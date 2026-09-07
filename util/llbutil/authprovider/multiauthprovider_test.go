package authprovider_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/util/llbutil/authprovider"
	"github.com/moby/buildkit/session/auth"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeChild struct {
	fetchTokenFn func(context.Context, *auth.FetchTokenRequest) (*auth.FetchTokenResponse, error)
	addProjectFn func(org, project string)
	fetchCount   atomic.Int32
}

func (*fakeChild) Credentials(_ context.Context, _ *auth.CredentialsRequest) (*auth.CredentialsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unimplemented")
}

func (f *fakeChild) FetchToken(ctx context.Context, req *auth.FetchTokenRequest) (*auth.FetchTokenResponse, error) {
	f.fetchCount.Add(1)

	if f.fetchTokenFn != nil {
		return f.fetchTokenFn(ctx, req)
	}

	return nil, authprovider.ErrAuthProviderNoResponse
}

func (*fakeChild) GetTokenAuthority(
	_ context.Context, _ *auth.GetTokenAuthorityRequest,
) (*auth.GetTokenAuthorityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unimplemented")
}

func (*fakeChild) VerifyTokenAuthority(
	_ context.Context, _ *auth.VerifyTokenAuthorityRequest,
) (*auth.VerifyTokenAuthorityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unimplemented")
}

func (f *fakeChild) AddProject(org, project string) {
	if f.addProjectFn != nil {
		f.addProjectFn(org, project)
	}
}

func newConsLogger() *conslogging.ConsoleLogger {
	return conslogging.New(os.Stderr, &sync.Mutex{}, 0, conslogging.Info, false)
}

func TestMultiAuth(t *testing.T) {
	t.Parallel()

	t.Run("it calls child ProjectAdders", func(t *testing.T) {
		t.Parallel()

		var calledOrg, calledProj string

		child := &fakeChild{
			addProjectFn: func(org, project string) {
				calledOrg = org
				calledProj = project
			},
		}

		multi := authprovider.New(newConsLogger(), []authprovider.Child{child})

		multi.AddProject("foo", "bar")

		require.Equal(t, "foo", calledOrg)
		require.Equal(t, "bar", calledProj)
	})

	t.Run("it does not continue to contact servers with no credentials for a given host", func(t *testing.T) {
		t.Parallel()

		child1 := &fakeChild{}
		child2 := &fakeChild{}

		multi := authprovider.New(newConsLogger(), []authprovider.Child{child1, child2})
		req := &auth.FetchTokenRequest{Host: "foo.bar"}

		// First call: both children checked
		resp, err := multi.FetchToken(t.Context(), req)
		require.Nil(t, resp)
		require.Equal(t, codes.Unavailable, status.Code(err))
		require.Equal(t, int32(1), child1.fetchCount.Load())
		require.Equal(t, int32(1), child2.fetchCount.Load())

		// Second call: skipped because host marked unavailable
		resp, err = multi.FetchToken(t.Context(), req)
		require.Nil(t, resp)
		require.Equal(t, codes.Unavailable, status.Code(err))
		require.Equal(t, int32(1), child1.fetchCount.Load())
		require.Equal(t, int32(1), child2.fetchCount.Load())
	})

	t.Run("it resets its knowledge of which servers it should contact after a project is added", func(t *testing.T) {
		t.Parallel()

		child1 := &fakeChild{}
		child2 := &fakeChild{
			fetchTokenFn: func(_ context.Context, _ *auth.FetchTokenRequest) (*auth.FetchTokenResponse, error) {
				return &auth.FetchTokenResponse{Token: "secret"}, nil
			},
		}

		multi := authprovider.New(newConsLogger(), []authprovider.Child{child1, child2})
		req := &auth.FetchTokenRequest{Host: "foo.bar"}

		resp, err := multi.FetchToken(t.Context(), req)
		require.NoError(t, err)
		require.Equal(t, "secret", resp.Token)
		require.Equal(t, int32(1), child1.fetchCount.Load())
		require.Equal(t, int32(1), child2.fetchCount.Load())

		// Reset via AddProject
		child2.fetchTokenFn = nil // now child2 will also return ErrAuthProviderNoResponse

		multi.AddProject("foo", "bar")

		resp, err = multi.FetchToken(t.Context(), req)
		require.Nil(t, resp)
		require.Equal(t, codes.Unavailable, status.Code(err))
		require.Equal(t, int32(2), child1.fetchCount.Load())
		require.Equal(t, int32(2), child2.fetchCount.Load())
	})
}
