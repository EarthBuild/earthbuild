package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

func newTestLogger(buf *bytes.Buffer) *conslogging.ConsoleLogger {
	return conslogging.New(buf, nil, 0, conslogging.Debug, false)
}

func TestLoggingFS_Walk(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	file1Path := filepath.Join(tmpDir, "file1.txt")
	file1Content := []byte("alpha")

	err := os.WriteFile(file1Path, file1Content, 0o600)
	require.NoError(t, err)

	subDir := filepath.Join(tmpDir, "subdir")

	err = os.Mkdir(subDir, 0o750)
	require.NoError(t, err)

	file2Path := filepath.Join(subDir, "file2.txt")
	file2Content := []byte("beta and gamma")

	err = os.WriteFile(file2Path, file2Content, 0o600)
	require.NoError(t, err)

	baseFS, err := fsutil.NewFS(tmpDir)
	require.NoError(t, err)

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	lfs := &loggingFS{
		FS:  baseFS,
		log: logger,
		dir: tmpDir,
	}

	walked := make(map[string]bool)

	err = lfs.Walk(t.Context(), "/", func(p string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		walked[p] = true

		return nil
	})
	require.NoError(t, err)

	walkLogs := buf.String()
	assert.Contains(t, walkLogs, "sent file stat for "+filepath.Join(tmpDir, "file1.txt")+"\n")
	assert.Contains(t, walkLogs, "sent file stat for "+filepath.Join(tmpDir, "subdir", "file2.txt")+"\n")
	assert.True(t, walked["file1.txt"])
	assert.True(t, walked[filepath.Join("subdir", "file2.txt")])
}

func TestLoggingFS_WalkCallbackErrorDoesNotLogStat(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "file1.txt")

	err := os.WriteFile(filePath, []byte("alpha"), 0o600)
	require.NoError(t, err)

	baseFS, err := fsutil.NewFS(tmpDir)
	require.NoError(t, err)

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	lfs := &loggingFS{
		FS:  baseFS,
		log: logger,
		dir: tmpDir,
	}

	wantErr := errors.New("send stat failed")

	err = lfs.Walk(t.Context(), "/", func(_ string, _ fs.DirEntry, _ error) error {
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)
	assert.Empty(t, buf.String())
}

type walkErrFS struct {
	fsutil.FS

	walkErr error
}

func (w *walkErrFS) Walk(_ context.Context, _ string, fn fs.WalkDirFunc) error {
	return fn("broken.txt", nil, w.walkErr)
}

func TestLoggingFS_WalkErrorSwallowedByCallbackDoesNotLogStat(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	lfs := &loggingFS{
		FS:  &walkErrFS{walkErr: errors.New("stat error")},
		log: logger,
		dir: tmpDir,
	}

	err := lfs.Walk(t.Context(), "/", func(_ string, _ fs.DirEntry, _ error) error {
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

type nonStatFS struct {
	fsutil.FS
}

func (n *nonStatFS) Open(p string) (io.ReadCloser, error) {
	rc, err := n.FS.Open(p)
	if err != nil {
		return nil, err
	}

	return struct{ io.ReadCloser }{rc}, nil
}

type errStatReadCloser struct {
	io.ReadCloser

	statErr error
}

func (e *errStatReadCloser) Stat() (fs.FileInfo, error) {
	return nil, e.statErr
}

type errStatFS struct {
	fsutil.FS

	statErr error
}

func (e *errStatFS) Open(p string) (io.ReadCloser, error) {
	rc, err := e.FS.Open(p)
	if err != nil {
		return nil, err
	}

	return &errStatReadCloser{
		ReadCloser: rc,
		statErr:    e.statErr,
	}, nil
}

func TestLoggingFS_Open(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wrapFS     func(fsutil.FS) fsutil.FS
		name       string
		wantSuffix string
		notWant    string
	}{
		{
			name:       "direct file with stat",
			wrapFS:     func(base fsutil.FS) fsutil.FS { return base },
			wantSuffix: " (14 B)\n",
		},
		{
			name:       "fallback without Stat",
			wrapFS:     func(base fsutil.FS) fsutil.FS { return &nonStatFS{FS: base} },
			wantSuffix: "\n",
			notWant:    "14 B",
		},
		{
			name: "fallback when Stat errors",
			wrapFS: func(base fsutil.FS) fsutil.FS {
				return &errStatFS{
					FS:      base,
					statErr: errors.New("stat failed"),
				}
			},
			wantSuffix: "\n",
			notWant:    "14 B",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			filePath := filepath.Join(tmpDir, "direct.txt")
			content := []byte("direct content")

			err := os.WriteFile(filePath, content, 0o600)
			require.NoError(t, err)

			baseFS, err := fsutil.NewFS(tmpDir)
			require.NoError(t, err)

			var buf bytes.Buffer

			lfs := &loggingFS{
				FS:  tt.wrapFS(baseFS),
				log: newTestLogger(&buf),
				dir: tmpDir,
			}

			rc, err := lfs.Open("direct.txt")
			require.NoError(t, err)

			data, err := io.ReadAll(rc)
			require.NoError(t, err)
			require.NoError(t, rc.Close())
			assert.Equal(t, content, data)

			want := "sent data for " + filePath + tt.wantSuffix
			assert.Contains(t, buf.String(), want)

			if tt.notWant != "" {
				assert.NotContains(t, buf.String(), tt.notWant)
			}
		})
	}

	t.Run("non-existent file", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()

		baseFS, err := fsutil.NewFS(tmpDir)
		require.NoError(t, err)

		var buf bytes.Buffer

		lfs := &loggingFS{
			FS:  baseFS,
			log: newTestLogger(&buf),
			dir: tmpDir,
		}

		rc, err := lfs.Open("does-not-exist.txt")
		require.Error(t, err)
		assert.Nil(t, rc)
		assert.Empty(t, buf.String())
	})
}
