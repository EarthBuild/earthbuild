package authprovider_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/util/llbutil/authprovider"
	"github.com/moby/buildkit/session/auth"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newConsLogger() conslogging.ConsoleLogger {
	return conslogging.New(os.Stderr, &sync.Mutex{}, conslogging.NoColor, 0, conslogging.Info, false)
}

func TestMultiAuth(t *testing.T) {
	t.Parallel()

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

		child := newMockChild()
		adder := newMockProjectAdder()
		p := projectProvider{
			mockChild:        child,
			mockProjectAdder: adder,
		}

		adder.On("AddProject", "foo", "bar").Return()

		multi := authprovider.New(newConsLogger(), []authprovider.Child{p})
		multi.AddProject("foo", "bar")

		adder.AssertExpectations(t)
	})

	t.Run("it does not continue to contact servers with no credentials for a given host", func(t *testing.T) {
		t.Parallel()

		children := []*mockChild{
			newMockChild(),
			newMockChild(),
		}

		srv := make([]authprovider.Child, 0, len(children))
		for _, c := range children {
			srv = append(srv, c)
		}

		multi := authprovider.New(newConsLogger(), srv)

		const host = "foo.bar"

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		res := make(chan fetchResult)

		// Set up expectations for the first call
		for _, c := range children {
			c.On("FetchToken", mock.Anything, &auth.FetchTokenRequest{Host: host}).
				Return((*auth.FetchTokenResponse)(nil), authprovider.ErrAuthProviderNoResponse)
		}

		go func() {
			resp, err := multi.FetchToken(ctx, &auth.FetchTokenRequest{Host: host})
			res <- fetchResult{resp, err}
		}()

		select {
		case result := <-res:
			if result.resp != nil {
				t.Error("expected response to be nil")
			}
			if status.Code(result.err) != codes.Unavailable {
				t.Errorf("expected error code to be Unavailable, got: %v", status.Code(result.err))
			}
		case <-time.After(timeout):
			t.Fatal("timed out waiting for FetchToken to return")
		}

		// Verify all mocks were called
		for _, c := range children {
			c.AssertExpectations(t)
		}

		// Second call should not contact servers again (cached)
		go func() {
			resp, err := multi.FetchToken(ctx, &auth.FetchTokenRequest{Host: host})
			res <- fetchResult{resp, err}
		}()

		select {
		case result := <-res:
			if result.resp != nil {
				t.Error("expected response to be nil")
			}
			if status.Code(result.err) != codes.Unavailable {
				t.Errorf("expected error code to be Unavailable, got: %v", status.Code(result.err))
			}
		case <-time.After(timeout):
			t.Fatal("timed out waiting for FetchToken to return")
		}

		// Verify mocks were NOT called again
		for _, c := range children {
			c.AssertNumberOfCalls(t, "FetchToken", 1)
		}
	})

	t.Run("it resets its knowledge of which servers it should contact after a project is added", func(t *testing.T) {
		t.Parallel()

		children := []*mockChild{
			newMockChild(),
			newMockChild(),
		}

		srv := make([]authprovider.Child, 0, len(children))
		for _, c := range children {
			srv = append(srv, c)
		}

		multi := authprovider.New(newConsLogger(), srv)

		const host = "foo.bar"

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		res := make(chan fetchResult)

		// First round: set up expectations where last child succeeds
		for i, c := range children {
			if i == len(children)-1 {
				// Last child returns success
				c.On("FetchToken", mock.Anything, &auth.FetchTokenRequest{Host: host}).
					Return(&auth.FetchTokenResponse{}, nil).Once()
			} else {
				c.On("FetchToken", mock.Anything, &auth.FetchTokenRequest{Host: host}).
					Return((*auth.FetchTokenResponse)(nil), authprovider.ErrAuthProviderNoResponse).Once()
			}
		}

		go func() {
			resp, err := multi.FetchToken(ctx, &auth.FetchTokenRequest{Host: host})
			res <- fetchResult{resp, err}
		}()

		select {
		case result := <-res:
			if status.Code(result.err) != codes.OK {
				t.Errorf("expected no error, got: %v", result.err)
			}
		case <-time.After(timeout):
			t.Fatal("timed out waiting for FetchToken to return")
		}

		// Verify first round expectations
		for _, c := range children {
			c.AssertExpectations(t)
		}

		// Add a project, which should reset the cache
		multi.AddProject("foo", "bar")

		// Second round: all children return no response
		for _, c := range children {
			c.On("FetchToken", mock.Anything, &auth.FetchTokenRequest{Host: host}).
				Return((*auth.FetchTokenResponse)(nil), authprovider.ErrAuthProviderNoResponse).Once()
		}

		go func() {
			resp, err := multi.FetchToken(ctx, &auth.FetchTokenRequest{Host: host})
			res <- fetchResult{resp, err}
		}()

		select {
		case result := <-res:
			if result.resp != nil {
				t.Error("expected response to be nil")
			}
			if status.Code(result.err) != codes.Unavailable {
				t.Errorf("expected error code to be Unavailable, got: %v", status.Code(result.err))
			}
		case <-time.After(timeout):
			t.Fatal("timed out waiting for FetchToken to return")
		}

		// Verify all mocks were called the expected number of times
		for _, c := range children {
			c.AssertExpectations(t)
		}
	})
}
