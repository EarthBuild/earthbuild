package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPortOffset(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		installationName string
		want             int
	}{
		{
			// The ports derived from a zero offset are hardcoded in
			// earth-entrypoint.sh and buildkitd/buildkitd.tcp.template, so the
			// official installation name must not be offset.
			name:             "official name is not offset",
			installationName: "earth",
			want:             0,
		},
		{
			// "earthly" is also an official installation name and must map to the
			// same zero offset as "earth".
			name:             "deprecated official name is not offset",
			installationName: "earthly",
			want:             0,
		},
		{
			name:             "dev name is offset",
			installationName: "earthly-dev",
			want:             178,
		},
		{
			name:             "offset is stable for a given name",
			installationName: "earth-dev",
			want:             783,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, PortOffset(tc.installationName))
		})
	}
}

func TestPortOffsetIsInRange(t *testing.T) {
	t.Parallel()

	// Offsets must stay small enough that the derived ports remain valid, and
	// non-zero so a dev installation cannot collide with an official one.
	for _, name := range []string{"earthly-dev", "earth-dev", "a", "some-very-long-installation-name"} {
		offset := PortOffset(name)
		assert.GreaterOrEqual(t, offset, 10, name)
		assert.Less(t, offset, 1010, name)
	}
}

// tempCfgDir returns a cfgDirFunc rooted in a fresh temp dir, so tests never
// resolve (or create) paths under the real installation config dir in $HOME.
func tempCfgDir(t *testing.T) (string, cfgDirFunc) {
	t.Helper()

	dir := t.TempDir()

	return dir, func() (string, error) { return dir, nil }
}

func TestSetTLSEnabled(t *testing.T) {
	t.Parallel()

	// Every TLS path is absolute, so neither ParseYAML nor setTLSEnabled needs
	// to resolve anything against an installation config dir.
	const absCfg = `
global:
  tls_enabled: %v
  tlsca: /certs/ca_cert.pem
  tlsca_key: /certs/ca_key.pem
  tlscert: /certs/earth_cert.pem
  tlskey: /certs/earth_key.pem
  buildkitd_tlscert: /certs/buildkit_cert.pem
  buildkitd_tlskey: /certs/buildkit_key.pem
`

	for _, tc := range []struct {
		name       string
		cfgEnabled bool
		override   bool
	}{
		{name: "flag disables TLS enabled in config", cfgEnabled: true, override: false},
		{name: "flag enables TLS disabled in config", cfgEnabled: false, override: true},
		{name: "flag agrees with config", cfgEnabled: true, override: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := ParseYAML(fmt.Appendf(nil, absCfg, tc.cfgEnabled), "earth-test")
			require.NoError(t, err)
			require.Equal(t, tc.cfgEnabled, cfg.Global.TLSEnabled)

			_, cfgDir := tempCfgDir(t)
			require.NoError(t, cfg.setTLSEnabled(tc.override, cfgDir))

			assert.Equal(t, tc.override, cfg.Global.TLSEnabled)
			assert.Equal(t, "/certs/ca_cert.pem", cfg.Global.TLSCACert)
			assert.Equal(t, "/certs/ca_key.pem", cfg.Global.TLSCAKey)
			assert.Equal(t, "/certs/earth_cert.pem", cfg.Global.ClientTLSCert)
			assert.Equal(t, "/certs/earth_key.pem", cfg.Global.ClientTLSKey)
			assert.Equal(t, "/certs/buildkit_cert.pem", cfg.Global.ServerTLSCert)
			assert.Equal(t, "/certs/buildkit_key.pem", cfg.Global.ServerTLSKey)
		})
	}
}

// Relative TLS paths are only resolved when TLS is enabled, so enabling it from
// the command line over a config file that disabled it must resolve them the
// same way ParseYAML would have.
func TestSetTLSEnabledResolvesRelativePaths(t *testing.T) {
	t.Parallel()

	// TLS is disabled, so ParseYAML leaves the relative defaults untouched and
	// never looks up the installation config dir.
	cfg, err := ParseYAML([]byte("global:\n  tls_enabled: false\n"), "earth-test")
	require.NoError(t, err)
	require.Equal(t, DefaultCACert, cfg.Global.TLSCACert, "paths are left alone while TLS is disabled")

	dir, cfgDir := tempCfgDir(t)
	require.NoError(t, cfg.setTLSEnabled(true, cfgDir))

	assert.True(t, cfg.Global.TLSEnabled)
	assert.Equal(t, filepath.Join(dir, DefaultCACert), cfg.Global.TLSCACert)
	assert.Equal(t, filepath.Join(dir, DefaultCAKey), cfg.Global.TLSCAKey)
	assert.Equal(t, filepath.Join(dir, DefaultClientTLSCert), cfg.Global.ClientTLSCert)
	assert.Equal(t, filepath.Join(dir, DefaultClientTLSKey), cfg.Global.ClientTLSKey)
	assert.Equal(t, filepath.Join(dir, DefaultServerTLSCert), cfg.Global.ServerTLSCert)
	assert.Equal(t, filepath.Join(dir, DefaultServerTLSKey), cfg.Global.ServerTLSKey)
}

// SetTLSEnabled(false) must not resolve (and so must not create) anything.
func TestSetTLSEnabledDisabledSkipsCfgDir(t *testing.T) {
	t.Parallel()

	cfg, err := ParseYAML([]byte("global:\n  tls_enabled: false\n"), "earth-test")
	require.NoError(t, err)

	called := false

	require.NoError(t, cfg.setTLSEnabled(false, func() (string, error) {
		called = true

		return "", errors.New("config dir must not be looked up")
	}))

	assert.False(t, called)
	assert.Equal(t, DefaultCACert, cfg.Global.TLSCACert)
}
