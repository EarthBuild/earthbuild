// Code generated manually to replace hel mocks. DO NOT EDIT by automated tools.
package proj_test

import (
	"context"
	"io"
	"io/fs"

	"github.com/EarthBuild/earthbuild/util/proj"
	"github.com/stretchr/testify/mock"
)

// mockFS is a mock implementation of FS interface
type mockFS struct {
	mock.Mock
}

func newMockFS() *mockFS {
	return &mockFS{}
}

func (m *mockFS) Open(name string) (fs.File, error) {
	args := m.Called(name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(fs.File), args.Error(1)
}

func (m *mockFS) Stat(name string) (fs.FileInfo, error) {
	args := m.Called(name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(fs.FileInfo), args.Error(1)
}

// mockCmd is a mock implementation of Cmd interface
type mockCmd struct {
	mock.Mock
}

func newMockCmd() *mockCmd {
	return &mockCmd{}
}

func (m *mockCmd) Run(ctx context.Context) (stdout, stderr io.Reader, err error) {
	args := m.Called(ctx)
	var stdoutReader io.Reader
	var stderrReader io.Reader

	if args.Get(0) != nil {
		stdoutReader = args.Get(0).(io.Reader)
	}
	if args.Get(1) != nil {
		stderrReader = args.Get(1).(io.Reader)
	}

	return stdoutReader, stderrReader, args.Error(2)
}

// mockExecer is a mock implementation of Execer interface
type mockExecer struct {
	mock.Mock
}

func newMockExecer() *mockExecer {
	return &mockExecer{}
}

func (m *mockExecer) Command(name string, args ...string) proj.Cmd {
	callArgs := []interface{}{name}
	for _, arg := range args {
		callArgs = append(callArgs, arg)
	}
	mockArgs := m.Called(callArgs...)
	if mockArgs.Get(0) == nil {
		return nil
	}
	return mockArgs.Get(0).(proj.Cmd)
}
