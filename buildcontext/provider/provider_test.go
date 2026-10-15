package provider

import (
	"bytes"
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

func TestLoggingFS_WalkAndOpen(t *testing.T) {
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
	require.Contains(t, walkLogs, "sent file stat for")

	// Verify filesize map.
	lfs.mu.Lock()
	size1, ok1 := lfs.filesize["file1.txt"]
	size2, ok2 := lfs.filesize["subdir/file2.txt"]
	_, hasDir := lfs.filesize["subdir"]
	lfs.mu.Unlock()

	require.True(t, ok1)
	require.Equal(t, int64(len(file1Content)), size1)
	require.True(t, ok2)
	require.Equal(t, int64(len(file2Content)), size2)
	require.False(t, hasDir)

	// Open file1.txt.
	buf.Reset()

	rc, err := lfs.Open("file1.txt")
	require.NoError(t, err)

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, string(file1Content), string(data))

	openLogs := buf.String()
	require.Contains(t, openLogs, "sent data for")
	require.Contains(t, openLogs, "5 B")
}

func TestLoggingFS_OpenFallbackWithoutWalk(t *testing.T) {
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

	want := "sent data for " + filepath.Join(tmpDir, "direct.txt") + "\n"
	require.Contains(t, buf.String(), want)
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
