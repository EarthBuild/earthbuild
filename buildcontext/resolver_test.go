package buildcontext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/EarthBuild/earthbuild/domain"
)

//nolint:paralleltest
func TestResolveLocalRootEarthfile(t *testing.T) {
	t.Run("finds Earthfile in grandparent directory (../../)", func(t *testing.T) {
		tmpDir := t.TempDir()

		err := os.WriteFile(filepath.Join(tmpDir, "Earthfile"), []byte("VERSION 0.8"), 0o600)
		require.NoError(t, err)

		subSubDir := filepath.Join(tmpDir, "sub", "subsub")

		err = os.MkdirAll(subSubDir, 0o700)
		require.NoError(t, err)

		t.Chdir(subSubDir)

		ref, err := domain.ParseTarget("+foo")
		require.NoError(t, err)

		resolved := resolveLocalRootEarthfile(ref)

		require.Equal(t, "../..", resolved.GetLocalPath())
	})

	t.Run("finds Earthfile in current directory (.)", func(t *testing.T) {
		tmpDir := t.TempDir()

		err := os.WriteFile(filepath.Join(tmpDir, "Earthfile"), []byte("VERSION 0.8"), 0o600)
		require.NoError(t, err)

		t.Chdir(tmpDir)

		ref, err := domain.ParseTarget("+foo")
		require.NoError(t, err)

		resolved := resolveLocalRootEarthfile(ref)

		require.Equal(t, ".", resolved.GetLocalPath())
	})

	t.Run("stops at nearest ancestor Earthfile", func(t *testing.T) {
		tmpDir := t.TempDir()

		err := os.WriteFile(filepath.Join(tmpDir, "Earthfile"), []byte("VERSION 0.8"), 0o600)
		require.NoError(t, err)

		withOwnDir := filepath.Join(tmpDir, "sub", "with_own_earthfile")

		err = os.MkdirAll(withOwnDir, 0o700)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(withOwnDir, "Earthfile"), []byte("VERSION 0.8"), 0o600)
		require.NoError(t, err)

		t.Chdir(withOwnDir)

		ref, err := domain.ParseTarget("+foo")
		require.NoError(t, err)

		resolved := resolveLocalRootEarthfile(ref)

		require.Equal(t, ".", resolved.GetLocalPath())
	})

	t.Run("returns original reference when no Earthfile exists in ancestors", func(t *testing.T) {
		tmpDir := t.TempDir()

		t.Chdir(tmpDir)

		ref, err := domain.ParseTarget("+foo")
		require.NoError(t, err)

		resolved := resolveLocalRootEarthfile(ref)

		require.Equal(t, ".", resolved.GetLocalPath())
	})
}
