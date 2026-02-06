// Code generated manually to replace hel mocks. DO NOT EDIT by automated tools.
package authprovider_test

import (
	"context"
	"io"
	"time"

	"github.com/moby/buildkit/session/auth"
	"github.com/stretchr/testify/mock"
)

// mockProjectAdder is a mock implementation of ProjectAdder interface
type mockProjectAdder struct {
	mock.Mock
}

func newMockProjectAdder() *mockProjectAdder {
	return &mockProjectAdder{}
}

func (m *mockProjectAdder) AddProject(org, project string) {
	m.Called(org, project)
}

// mockChild is a mock implementation of Child interface
type mockChild struct {
	mock.Mock
}

func newMockChild() *mockChild {
	return &mockChild{}
}

func (m *mockChild) Credentials(ctx context.Context, req *auth.CredentialsRequest) (*auth.CredentialsResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.CredentialsResponse), args.Error(1)
}

func (m *mockChild) FetchToken(ctx context.Context, req *auth.FetchTokenRequest) (*auth.FetchTokenResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.FetchTokenResponse), args.Error(1)
}

func (m *mockChild) GetTokenAuthority(ctx context.Context, req *auth.GetTokenAuthorityRequest) (*auth.GetTokenAuthorityResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.GetTokenAuthorityResponse), args.Error(1)
}

func (m *mockChild) VerifyTokenAuthority(ctx context.Context, req *auth.VerifyTokenAuthorityRequest) (*auth.VerifyTokenAuthorityResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*auth.VerifyTokenAuthorityResponse), args.Error(1)
}

// mockOS is a mock implementation of OS interface
type mockOS struct {
	mock.Mock
}

func newMockOS() *mockOS {
	return &mockOS{}
}

func (m *mockOS) Open(name string) (io.ReadCloser, error) {
	args := m.Called(name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *mockOS) Getenv(key string) string {
	args := m.Called(key)
	return args.String(0)
}

// mockWriter is a mock implementation of io.Writer
type mockWriter struct {
	mock.Mock
}

func newMockWriter() *mockWriter {
	return &mockWriter{}
}

func (m *mockWriter) Write(p []byte) (n int, err error) {
	args := m.Called(p)
	return args.Int(0), args.Error(1)
}

// Helper constants for test timeouts
const (
	timeout     = time.Second
	mockTimeout = 5 * time.Second
)
