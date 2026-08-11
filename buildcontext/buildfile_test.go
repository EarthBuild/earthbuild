package buildcontext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/stretchr/testify/require"
)

func TestNewDockerfileBuild(t *testing.T) {
	t.Parallel()

	bf := newDockerfileBuild("Dockerfile")
	require.NotNil(t, bf)
	require.Equal(t, "Dockerfile", bf.path)
	require.NotNil(t, bf.features)
}

func TestNewEarthfileBuild(t *testing.T) {
	t.Parallel()

	console := conslogging.Current(conslogging.NoPadding, conslogging.Silent, false)

	t.Run("valid Earthfile", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "Earthfile")
		err := os.WriteFile(path, []byte("VERSION 0.8\n\ntest:\n\tFROM alpine:3.24.1\n"), 0o600)
		require.NoError(t, err)

		bf, err := newEarthfileBuild(path, "", console)
		require.NoError(t, err)
		require.Equal(t, path, bf.path)
		require.NotNil(t, bf.features)
		require.NotNil(t, bf.tree.Version)
		require.Equal(t, []string{"0.8"}, bf.tree.Version.Args)
	})

	t.Run("warning for redundant feature flag in version", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "Earthfile")
		err := os.WriteFile(path, []byte("VERSION --for-in 0.8\n\ntest:\n\tFROM alpine:3.24.1\n"), 0o600)
		require.NoError(t, err)

		bf, err := newEarthfileBuild(path, "", console)
		require.NoError(t, err)
		require.NotNil(t, bf.features)
	})

	t.Run("missing version error", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "Earthfile")
		err := os.WriteFile(path, []byte("test:\n\tFROM alpine:3.24.1\n"), 0o600)
		require.NoError(t, err)

		_, err = newEarthfileBuild(path, "", console)
		require.Error(t, err)
		require.ErrorContains(t, err, "no version specified")
	})

	t.Run("apply feature flag overrides", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "Earthfile")
		err := os.WriteFile(path, []byte("VERSION 0.7\n\ntest:\n\tFROM alpine:3.24.1\n"), 0o600)
		require.NoError(t, err)

		bf, err := newEarthfileBuild(path, "no-implicit-ignore", console)
		require.NoError(t, err)
		require.True(t, bf.features.NoImplicitIgnore)
	})

	t.Run("invalid feature flag overrides error", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "Earthfile")
		err := os.WriteFile(path, []byte("VERSION 0.7\n\ntest:\n\tFROM alpine:3.24.1\n"), 0o600)
		require.NoError(t, err)

		_, err = newEarthfileBuild(path, "invalid-flag-name", console)
		require.ErrorContains(t, err, "unable to set invalid-flag-name: invalid flag")
	})
}
