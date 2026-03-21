package proj_test

import (
	"bytes"
	"context"
	"io/fs"
	"testing"
	"time"

	"git.sr.ht/~nelsam/hel/pkg/pers"
	"github.com/EarthBuild/earthbuild/util/proj"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

const (
	timeout     = time.Second
	mockTimeout = 5 * time.Second
)

func TestGolang(t *testing.T) {
	t.Parallel()

	type testCtx struct {
		golang *proj.Golang

		fs   *mockFS
		exec *mockExecer
	}

	setup := func(t *testing.T) (context.Context, testCtx) {
		t.Helper()

		fsMock := newMockFS(pers.WithTimeout(t, mockTimeout))
		execMock := newMockExecer(pers.WithTimeout(t, mockTimeout))
		ctx, cancel := context.WithTimeout(t.Context(), timeout)
		t.Cleanup(cancel)

		return ctx, testCtx{
			golang: proj.NewGolang(fsMock, execMock),
			fs:     fsMock,
			exec:   execMock,
		}
	}

	t.Run("Type", func(t *testing.T) {
		t.Parallel()

		ctx, tc := setup(t)
		require.Equal(t, "go", tc.golang.Type(ctx))
	})

	t.Run("ForDir", func(t *testing.T) {
		t.Parallel()

		t.Run("it skips projects without a go.mod", func(t *testing.T) {
			t.Parallel()

			ctx, tc := setup(t)
			pers.Return(tc.fs.method.Stat, nil, fs.ErrNotExist)
			_, err := tc.golang.ForDir(ctx, ".")

			pers.MethodWasCalled(t, tc.fs.method.Stat, pers.WithArgs("go.mod"))
			require.ErrorIs(t, err, proj.ErrSkip)
		})

		t.Run("it errors if reading go.mod fails", func(t *testing.T) {
			t.Parallel()

			ctx, tc := setup(t)
			boom := errors.New("boom")
			pers.Return(tc.fs.method.Stat, nil, boom)
			_, err := tc.golang.ForDir(ctx, ".")

			pers.MethodWasCalled(t, tc.fs.method.Stat, pers.WithArgs("go.mod"))
			require.Error(t, err)
			require.NotErrorIs(t, err, proj.ErrSkip)
		})

		t.Run("it errors if 'go' is not available", func(t *testing.T) {
			t.Parallel()

			ctx, tc := setup(t)
			pers.Return(tc.fs.method.Stat, nil, nil)

			cmd := newMockCmd(pers.WithTimeout(t, mockTimeout))
			pers.Return(tc.exec.method.Command, cmd)

			const projDir = "some/path/to/a/project"

			stdout := bytes.NewBufferString(projDir)
			pers.Return(cmd.method.Run, stdout, nil, fs.ErrNotExist)

			_, err := tc.golang.ForDir(ctx, ".")

			pers.MethodWasCalled(t, tc.fs.method.Stat, pers.WithArgs("go.mod"))
			pers.MethodWasCalled(t, tc.exec.method.Command, pers.WithArgs("go", "list", "-f", "{{.Dir}}"))
			pers.MethodWasCalled(t, cmd.method.Run)
			require.Error(t, err)
			require.NotErrorIs(t, err, proj.ErrSkip)
		})
	})
}
