package proj_test

import (
	"bytes"
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/util/proj"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/mock"
)

const (
	timeout     = time.Second
	mockTimeout = 5 * time.Second
)

func TestGolang(t *testing.T) {
	t.Parallel()

	t.Run("Type", func(t *testing.T) {
		t.Parallel()

		mockFS := newMockFS()
		exec := newMockExecer()

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		golang := proj.NewGolang(mockFS, exec)
		result := golang.Type(ctx)

		if result != "go" {
			t.Errorf("expected Type to return %q, got %q", "go", result)
		}
	})

	t.Run("ForDir", func(t *testing.T) {
		t.Parallel()

		t.Run("it skips projects without a go.mod", func(t *testing.T) {
			t.Parallel()

			mockFS := newMockFS()
			exec := newMockExecer()

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			golang := proj.NewGolang(mockFS, exec)
			mockFS.On("Stat", "go.mod").Return(nil, fs.ErrNotExist)

			_, err := golang.ForDir(ctx, ".")

			if !errors.Is(err, proj.ErrSkip) {
				t.Errorf("expected error to be proj.ErrSkip, got: %v", err)
			}

			mockFS.AssertExpectations(t)
		})

		t.Run("it errors if reading go.mod fails", func(t *testing.T) {
			t.Parallel()

			mockFS := newMockFS()
			exec := newMockExecer()

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			golang := proj.NewGolang(mockFS, exec)
			mockFS.On("Stat", "go.mod").Return(nil, errors.New("boom"))

			_, err := golang.ForDir(ctx, ".")

			if err == nil {
				t.Error("expected an error to occur")
			}

			if errors.Is(err, proj.ErrSkip) {
				t.Error("expected error not to be proj.ErrSkip")
			}

			mockFS.AssertExpectations(t)
		})

		t.Run("it errors if 'go' is not available", func(t *testing.T) {
			t.Parallel()

			mockFS := newMockFS()
			exec := newMockExecer()

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			golang := proj.NewGolang(mockFS, exec)
			mockFS.On("Stat", "go.mod").Return(nil, nil)

			cmd := newMockCmd()
			exec.On("Command", "go", "list", "-f", "{{.Dir}}").Return(cmd)

			const projDir = "some/path/to/a/project"

			stdout := bytes.NewBufferString(projDir)
			cmd.On("Run", mock.Anything).Return(stdout, nil, fs.ErrNotExist)

			_, err := golang.ForDir(ctx, ".")

			if err == nil {
				t.Error("expected an error to occur")
			}

			if errors.Is(err, proj.ErrSkip) {
				t.Error("expected error not to be proj.ErrSkip")
			}

			mockFS.AssertExpectations(t)
			exec.AssertExpectations(t)
			cmd.AssertExpectations(t)
		})
	})
}
