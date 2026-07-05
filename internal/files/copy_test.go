package files

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopy(t *testing.T) {
	t.Parallel()

	// Create temporary directory for tests
	tmpDir := t.TempDir()

	srcDir := filepath.Join(tmpDir, "src")
	dstDir := filepath.Join(tmpDir, "dst")

	err := os.Mkdir(srcDir, 0o755) // #nosec G301
	require.NoError(t, err)

	// 1. Create a nested file structure
	file1 := filepath.Join(srcDir, "file1.txt")
	err = os.WriteFile(file1, []byte("hello world"), 0o644) // #nosec G306
	require.NoError(t, err)

	execFile := filepath.Join(srcDir, "exec.sh")
	err = os.WriteFile(execFile, []byte("#!/bin/sh\necho hi"), 0o755) // #nosec G306
	require.NoError(t, err)

	subDir := filepath.Join(srcDir, "sub")
	err = os.Mkdir(subDir, 0o700)
	require.NoError(t, err)

	file2 := filepath.Join(subDir, "file2.txt")
	err = os.WriteFile(file2, []byte("nested"), 0o600)
	require.NoError(t, err)

	// Create symlink: link to file1.txt
	linkName := filepath.Join(srcDir, "link.txt")
	err = os.Symlink("file1.txt", linkName)
	require.NoError(t, err)

	// 2. Perform the Copy
	err = Copy(srcDir, dstDir)
	require.NoError(t, err)

	// 3. Verify copy content and permissions
	// Check dstDir mode
	dstInfo, err := os.Stat(dstDir)
	require.NoError(t, err)
	require.True(t, dstInfo.IsDir())

	// Check file1
	dstFile1 := filepath.Join(dstDir, "file1.txt")
	content1, err := os.ReadFile(dstFile1) // #nosec G304
	require.NoError(t, err)
	require.Equal(t, "hello world", string(content1))

	fi1, err := os.Stat(dstFile1)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi1.Mode().Perm())

	// Check execFile
	dstExecFile := filepath.Join(dstDir, "exec.sh")
	contentExec, err := os.ReadFile(dstExecFile) // #nosec G304
	require.NoError(t, err)
	require.Equal(t, "#!/bin/sh\necho hi", string(contentExec))

	fiExec, err := os.Stat(dstExecFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), fiExec.Mode().Perm())

	// Check subDir
	dstSubDir := filepath.Join(dstDir, "sub")
	fiSub, err := os.Stat(dstSubDir)
	require.NoError(t, err)
	require.True(t, fiSub.IsDir())
	require.Equal(t, os.FileMode(0o700), fiSub.Mode().Perm())

	// Check file2
	dstFile2 := filepath.Join(dstSubDir, "file2.txt")
	content2, err := os.ReadFile(dstFile2) // #nosec G304
	require.NoError(t, err)
	require.Equal(t, "nested", string(content2))

	fi2, err := os.Stat(dstFile2)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi2.Mode().Perm())

	// Check symlink
	dstLink := filepath.Join(dstDir, "link.txt")
	fiLink, err := os.Lstat(dstLink)
	require.NoError(t, err)
	require.NotEqual(t, os.FileMode(0), fiLink.Mode()&os.ModeSymlink)

	target, err := os.Readlink(dstLink)
	require.NoError(t, err)
	require.Equal(t, "file1.txt", target)
}

func TestCopySingleFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	srcFile := filepath.Join(tmpDir, "src.txt")
	dstFile := filepath.Join(tmpDir, "dst.txt")

	err := os.WriteFile(srcFile, []byte("single file content"), 0o644) // #nosec G306
	require.NoError(t, err)

	err = Copy(srcFile, dstFile)
	require.NoError(t, err)

	content, err := os.ReadFile(dstFile) // #nosec G304
	require.NoError(t, err)
	require.Equal(t, "single file content", string(content))
}
