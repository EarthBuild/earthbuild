package subcmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// write creates dir and puts an Earthfile-ish file called name in it.
func write(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("VERSION 0.8\n"), 0o600))
}

// `ls` locates a build file the way the resolver it replaced did: a caller who
// names no directory is inside a project rather than at its root, and a project
// may still call its build file build.earth.
//
//nolint:paralleltest // defined in terms of the working directory
func TestFindBuildFile(t *testing.T) {
	t.Run("an ancestor's Earthfile, from a nested directory", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "Earthfile")
		require.NoError(t, os.MkdirAll(filepath.Join(root, "sub", "subsub"), 0o700))
		t.Chdir(filepath.Join(root, "sub", "subsub"))

		got, err := findBuildFile("")
		require.NoError(t, err)
		require.Equal(t, filepath.Join("..", "..", "Earthfile"), got)
	})

	t.Run("the nearest ancestor wins", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "Earthfile")
		own := filepath.Join(root, "sub", "own")
		write(t, own, "Earthfile")
		t.Chdir(own)

		got, err := findBuildFile("")
		require.NoError(t, err)
		require.Equal(t, "Earthfile", got)
	})

	t.Run("build.earth, where there is no Earthfile", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "build.earth")
		t.Chdir(root)

		got, err := findBuildFile("")
		require.NoError(t, err)
		require.Equal(t, "build.earth", got)
	})

	t.Run("a named directory is read, not searched upwards", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "Earthfile")
		require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o700))
		t.Chdir(root)

		got, err := findBuildFile("./sub")
		require.Error(t, err, "a named directory that holds nothing is an error, not a walk to %s", got)

		got, err = findBuildFile("./empty")
		require.Error(t, err, "a named directory that holds nothing is an error, not a walk to %s", got)
	})

	t.Run("a named directory holding a build file", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "sub"), "build.earth")
		t.Chdir(root)

		got, err := findBuildFile("./sub")
		require.NoError(t, err)
		require.Equal(t, filepath.Join("sub", "build.earth"), got)
	})

	t.Run("nothing anywhere", func(t *testing.T) {
		t.Chdir(t.TempDir())

		_, err := findBuildFile("")
		require.Error(t, err)
	})
}
