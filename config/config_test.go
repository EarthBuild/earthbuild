package config

import (
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

func TestSetTLSEnabled(t *testing.T) {
	t.Parallel()

	// Absolute paths keep the test away from the installation's config dir.
	const absCfg = `
global:
  tls_enabled: %v
  tlsca: /certs/ca_cert.pem
  tlscert: /certs/earth_cert.pem
  tlskey: /certs/earth_key.pem
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

			require.NoError(t, cfg.SetTLSEnabled("earth-test", tc.override))

			assert.Equal(t, tc.override, cfg.Global.TLSEnabled)
			assert.Equal(t, "/certs/ca_cert.pem", cfg.Global.TLSCACert)
			assert.Equal(t, "/certs/earth_cert.pem", cfg.Global.ClientTLSCert)
			assert.Equal(t, "/certs/earth_key.pem", cfg.Global.ClientTLSKey)
		})
	}
}

// Relative TLS paths are only resolved when TLS is enabled, so enabling it from
// the command line over a config file that disabled it must resolve them the
// same way ParseYAML would have.
func TestSetTLSEnabledResolvesRelativePaths(t *testing.T) {
	// Do not use t.Parallel() because t.Setenv modifies process-wide state.
	home := t.TempDir()
	t.Setenv("HOME", home)

	const instName = "earth-tls-test"

	cfg, err := ParseYAML([]byte("global:\n  tls_enabled: false\n"), instName)
	require.NoError(t, err)
	require.Equal(t, DefaultCACert, cfg.Global.TLSCACert, "paths are left alone while TLS is disabled")

	require.NoError(t, cfg.SetTLSEnabled(instName, true))

	assert.True(t, cfg.Global.TLSEnabled)

	want := filepath.Join(home, "."+instName)
	assert.Equal(t, filepath.Join(want, DefaultCACert), cfg.Global.TLSCACert)
	assert.Equal(t, filepath.Join(want, DefaultClientTLSCert), cfg.Global.ClientTLSCert)
	assert.Equal(t, filepath.Join(want, DefaultClientTLSKey), cfg.Global.ClientTLSKey)
}
