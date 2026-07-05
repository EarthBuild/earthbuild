//go:build integration

package cli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// NOTE: this file covers the temporary EARTHLY_ -> EARTH_ environment variable
// migration. Remove it once EARTHLY_ support is officially dropped from the
// codebase.

func writeEmptyConfig(t *testing.T, home string) string {
	t.Helper()
	cfgPath := filepath.Join(home, ".earthly", "other-config.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath, nil, 0o600))
	return cfgPath
}

// TestDeprecatedEarthlyEnvVarWarns asserts that using a legacy EARTHLY_ prefixed
// environment variable still works but logs a deprecation warning.
func TestDeprecatedEarthlyEnvVarWarns(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := t.TempDir()
	cfgPath := writeEmptyConfig(t, home)

	out, err := runEarthWithEnv(
		t,
		projectDir,
		[]string{"HOME=" + home, "EARTHLY_CONFIG=" + cfgPath},
		"config",
		"global.cache_size_mb",
		"10",
	)
	require.NoError(t, err, out)
	require.Contains(t, out, "WARNING: EARTHLY_CONFIG is deprecated. Use EARTH_CONFIG.")
}

// TestEarthEnvVarDoesNotWarn asserts that the current EARTH_ prefixed
// environment variable works without emitting a deprecation warning.
func TestEarthEnvVarDoesNotWarn(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectDir := t.TempDir()
	cfgPath := writeEmptyConfig(t, home)

	out, err := runEarthWithEnv(
		t,
		projectDir,
		[]string{"HOME=" + home, "EARTH_CONFIG=" + cfgPath},
		"config",
		"global.cache_size_mb",
		"10",
	)
	require.NoError(t, err, out)
	require.NotContains(t, out, "is deprecated")
}
