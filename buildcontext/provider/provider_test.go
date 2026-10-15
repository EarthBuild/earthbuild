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
	require.Contains(t, walkLogs, "sent file stat for "+filepath.Join(tmpDir, "file1.txt")+"\n")
	require.Contains(t, walkLogs, "sent file stat for "+filepath.Join(tmpDir, "subdir", "file2.txt")+"\n")
	require.True(t, walked["file1.txt"])
	require.True(t, walked[filepath.Join("subdir", "file2.txt")])
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
	require.Empty(t, buf.String())
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
	require.Empty(t, buf.String())
}

func TestLoggingFS_OpenDirect(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "direct.txt")
	content := []byte("direct content")

	err := os.WriteFile(filePath, content, 0o600)
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

	rc, err := lfs.Open("direct.txt")
	require.NoError(t, err)

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, string(content), string(data))

	want := "sent data for " + filepath.Join(tmpDir, "direct.txt") + " (14 B)\n"
	require.Contains(t, buf.String(), want)
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

func TestLoggingFS_OpenFallbackWithoutStat(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "direct.txt")
	content := []byte("direct content")

	err := os.WriteFile(filePath, content, 0o600)
	require.NoError(t, err)

	baseFS, err := fsutil.NewFS(tmpDir)
	require.NoError(t, err)

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	lfs := &loggingFS{
		FS:  &nonStatFS{FS: baseFS},
		log: logger,
		dir: tmpDir,
	}

	rc, err := lfs.Open("direct.txt")
	require.NoError(t, err)

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, string(content), string(data))

	want := "sent data for " + filepath.Join(tmpDir, "direct.txt") + "\n"
	require.Contains(t, buf.String(), want)
	require.NotContains(t, buf.String(), "14 B")
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

func TestLoggingFS_OpenFallbackWhenStatErrors(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "direct.txt")
	content := []byte("direct content")

	err := os.WriteFile(filePath, content, 0o600)
	require.NoError(t, err)

	baseFS, err := fsutil.NewFS(tmpDir)
	require.NoError(t, err)

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	lfs := &loggingFS{
		FS: &errStatFS{
			FS:      baseFS,
			statErr: errors.New("stat failed"),
		},
		log: logger,
		dir: tmpDir,
	}

	rc, err := lfs.Open("direct.txt")
	require.NoError(t, err)

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, string(content), string(data))

	want := "sent data for " + filepath.Join(tmpDir, "direct.txt") + "\n"
	require.Contains(t, buf.String(), want)
	require.NotContains(t, buf.String(), "14 B")
}

func TestLoggingFS_OpenNonExistent(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	baseFS, err := fsutil.NewFS(tmpDir)
	require.NoError(t, err)

	var buf bytes.Buffer

	logger := newTestLogger(&buf)
	lfs := &loggingFS{
		FS:  baseFS,
		log: logger,
		dir: tmpDir,
	}

	rc, err := lfs.Open("does-not-exist.txt")
	require.Error(t, err)
	require.Nil(t, rc)
}
