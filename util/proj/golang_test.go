package proj_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/EarthBuild/earthbuild/util/proj"
)

type fakeFS struct {
	statErr error
}

func (fakeFS) Open(_ string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (f fakeFS) Stat(_ string) (fs.FileInfo, error) {
	return nil, f.statErr
}

type fakeCmd struct {
	err    error
	stdout string
}

func (c fakeCmd) Run(_ context.Context) (io.Reader, io.Reader, error) {
	return bytes.NewBufferString(c.stdout), nil, c.err
}

type fakeExecer fakeCmd

func (e fakeExecer) Command(_ string, _ ...string) proj.Cmd {
	return fakeCmd(e)
}

func TestGolang_Type(t *testing.T) {
	t.Parallel()

	g := proj.NewGolang(fakeFS{}, fakeExecer{})
	require.Equal(t, "go", g.Type(t.Context()))
}

func TestGolang_ForDir(t *testing.T) {
	t.Parallel()

	type testCase struct {
		execer    fakeExecer
		wantErrIs error
		fs        fakeFS
		name      string
		wantRoot  string
		wantErr   bool
	}

	tests := []testCase{
		{
			name:      "skips projects without go.mod",
			fs:        fakeFS{statErr: fs.ErrNotExist},
			wantErr:   true,
			wantErrIs: proj.ErrSkip,
		},
		{
			name:    "errors if reading go.mod fails",
			fs:      fakeFS{statErr: errors.New("boom")},
			wantErr: true,
		},
		{
			name:    "errors if go command fails",
			fs:      fakeFS{statErr: nil},
			execer:  fakeExecer{err: fs.ErrNotExist},
			wantErr: true,
		},
		{
			name:     "returns project when go.mod exists and go command succeeds",
			fs:       fakeFS{statErr: nil},
			execer:   fakeExecer{stdout: "/path/to/project"},
			wantRoot: func() string { abs, _ := filepath.Abs("/path/to/project"); return abs }(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := proj.NewGolang(tt.fs, tt.execer)
			p, err := g.ForDir(t.Context(), ".")

			if tt.wantErr {
				require.Error(t, err)

				if tt.wantErrIs != nil {
					require.ErrorIs(t, err, tt.wantErrIs)
				} else {
					require.NotErrorIs(t, err, proj.ErrSkip)
				}

				return
			}

			require.NoError(t, err)
			require.NotNil(t, p)
			require.Equal(t, tt.wantRoot, p.Root(t.Context()))
		})
	}
}
