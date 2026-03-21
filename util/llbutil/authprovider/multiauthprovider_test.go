package authprovider_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~nelsam/hel/pkg/pers"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/util/llbutil/authprovider"
	"github.com/moby/buildkit/session/auth"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newConsLogger() conslogging.ConsoleLogger {
	return conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)
}

func TestMultiAuth(t *testing.T) {
	t.Parallel()

	type testCtx struct {
		multi    *authprovider.MultiAuthProvider
		children []*mockChild
	}

	setup := func(t *testing.T) testCtx {
		t.Helper()

		children := []*mockChild{
			newMockChild(pers.WithTimeout(t, mockTimeout)),
			newMockChild(pers.WithTimeout(t, mockTimeout)),
		}

		srv := make([]authprovider.Child, 0, len(children))
		for _, c := range children {
			srv = append(srv, c)
		}

		return testCtx{
			children: children,
			multi:    authprovider.New(newConsLogger(), srv),
		}
	}

	type fetchResult struct {
		resp *auth.FetchTokenResponse
		err  error
	}

	t.Run("it calls child ProjectAdders", func(t *testing.T) {
		t.Parallel()

		type projectProvider struct {
			*mockChild
			*mockProjectAdder
		}

		p := projectProvider{
			mockChild:        newMockChild(pers.WithTimeout(t, mockTimeout)),
			mockProjectAdder: newMockProjectAdder(pers.WithTimeout(t, mockTimeout)),
		}
		multi := authprovider.New(newConsLogger(), []authprovider.Child{p})
		pers.Return(p.mockProjectAdder.method.AddProject)
		multi.AddProject("foo", "bar")
		pers.MethodWasCalled(t, p.mockProjectAdder.method.AddProject, pers.WithArgs("foo", "bar"))
	})

	t.Run("it does not continue to contact servers with no credentials for a given host", func(t *testing.T) {
		t.Parallel()

		tc := setup(t)
		req := &auth.FetchTokenRequest{Host: "foo.bar"}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		res := make(chan fetchResult)

		go func() {
			resp, err := tc.multi.FetchToken(ctx, req)
			res <- fetchResult{resp, err}
		}()

		for _, c := range tc.children {
			pers.MethodWasCalled(t, c.method.FetchToken,
				pers.Within(timeout),
				pers.WithArgs(pers.Any, req),
				pers.Returning((*auth.FetchTokenResponse)(nil), authprovider.ErrAuthProviderNoResponse),
			)
		}

		select {
		case result := <-res:
			require.Nil(t, result.resp)
			require.Equal(t, codes.Unavailable, status.Code(result.err))
		case <-time.After(timeout):
			t.Fatal("timed out waiting for FetchToken to return")
		}

		go func() {
			resp, err := tc.multi.FetchToken(ctx, req)
			res <- fetchResult{resp, err}
		}()

		for _, c := range tc.children {
			pers.MethodWasNotCalled(t, c.method.FetchToken, "FetchToken", pers.Within(10*time.Millisecond))
		}

		select {
		case result := <-res:
			require.Nil(t, result.resp)
			require.Equal(t, codes.Unavailable, status.Code(result.err))
		case <-time.After(timeout):
			t.Fatal("timed out waiting for FetchToken to return")
		}
	})

	t.Run("it resets its knowledge of which servers it should contact after a project is added", func(t *testing.T) {
		t.Parallel()

		tc := setup(t)
		req := &auth.FetchTokenRequest{Host: "foo.bar"}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		res := make(chan fetchResult)

		go func() {
			resp, err := tc.multi.FetchToken(ctx, req)
			res <- fetchResult{resp, err}
		}()

		for i, c := range tc.children {
			ret := []any{
				(*auth.FetchTokenResponse)(nil),
				authprovider.ErrAuthProviderNoResponse,
			}
			if i == len(tc.children)-1 {
				ret = []any{
					&auth.FetchTokenResponse{},
					nil,
				}
			}

			pers.MethodWasCalled(t, c.method.FetchToken,
				pers.Within(timeout),
				pers.WithArgs(pers.Any, req),
				pers.Returning(ret...),
			)
		}

		select {
		case result := <-res:
			require.NoError(t, result.err)
		case <-time.After(timeout):
			t.Fatal("timed out waiting for FetchToken to return")
		}

		tc.multi.AddProject("foo", "bar")

		go func() {
			resp, err := tc.multi.FetchToken(ctx, req)
			res <- fetchResult{resp, err}
		}()

		for _, c := range tc.children {
			pers.MethodWasCalled(t, c.method.FetchToken,
				pers.Within(timeout),
				pers.WithArgs(pers.Any, req),
				pers.Returning((*auth.FetchTokenResponse)(nil), authprovider.ErrAuthProviderNoResponse),
			)
		}

		select {
		case result := <-res:
			require.Nil(t, result.resp)
			require.Equal(t, codes.Unavailable, status.Code(result.err))
		case <-time.After(timeout):
			t.Fatal("timed out waiting for FetchToken to return")
		}
	})
}
