package buildkitd

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
)

// buildkitEnv is the only thing that decides what buildkitd runs with, so the whole
// map is asserted - a key silently dropped while moving these out of Start would
// otherwise only surface as a behaviour change in a running daemon.
//
//nolint:paralleltest // clearOTELEnv calls t.Setenv, which forbids t.Parallel.
func TestBuildkitEnv(t *testing.T) {
	const (
		enabled  = "true"
		disabled = "false"
	)

	// wantEnv is the environment buildkitd always gets, plus whatever the optional
	// settings add. The keys are spelled out once: they are the contract with the
	// buildkitd image, so the test must not share a constant with the code under test.
	wantEnv := func(debug, tcp, tls, maxParallelism string, extra map[string]string) map[string]string {
		env := map[string]string{
			"BUILDKIT_DEBUG":                 debug,
			"BUILDKIT_TCP_TRANSPORT_ENABLED": tcp,
			"BUILDKIT_TLS_ENABLED":           tls,
			"BUILDKIT_MAX_PARALLELISM":       maxParallelism,
		}
		maps.Copy(env, extra)

		return env
	}

	tests := []struct {
		want     map[string]string
		name     string
		settings Settings
		reset    bool
	}{
		{
			name: "zero settings",
			want: wantEnv(disabled, disabled, disabled, "0", nil),
		},
		{
			// TLS is only enabled alongside TCP: UseTLS on its own must not reach buildkitd.
			name:     "tls without tcp",
			settings: Settings{UseTLS: true},
			want:     wantEnv(disabled, disabled, disabled, "0", nil),
		},
		{
			name:  "every optional setting",
			reset: true,
			settings: Settings{
				Debug:             true,
				UseTCP:            true,
				UseTLS:            true,
				MaxParallelism:    4,
				AdditionalConfig:  "[worker]",
				IPTables:          "nf_tables",
				CniMtu:            1400,
				CacheSizeMb:       100,
				CacheSizePct:      50,
				CacheKeepDuration: 300,
				EnableProfiler:    true,
			},
			want: wantEnv(enabled, enabled, enabled, "4", map[string]string{
				"EARTH_ADDITIONAL_BUILDKIT_CONFIG": "[worker]",
				"IP_TABLES":                        "nf_tables",
				"CNI_MTU":                          "1400",
				"CACHE_SIZE_MB":                    "100",
				"CACHE_SIZE_PCT":                   "50",
				"CACHE_KEEP_DURATION":              "300",
				"BUILDKIT_PPROF_ENABLED":           enabled,
				"EARTH_RESET_TMP_DIR":              enabled,
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearOTELEnv(t)

			got := buildkitEnv(test.settings, "earthly-buildkitd", "earthly", false, test.reset)

			assert.Equal(t, test.want, got)
		})
	}
}

// The telemetry env has to survive the merge into the rest of the environment.
func TestBuildkitEnvIncludesTelemetry(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel:4317")

	got := buildkitEnv(Settings{MaxParallelism: 3}, "earthly-buildkitd", "earthly", false, false)

	assert.Equal(t, "http://otel:4317", got["OTEL_EXPORTER_OTLP_ENDPOINT"])
	assert.Equal(t, buildkitdOTELServiceName, got["OTEL_SERVICE_NAME"])
	assert.Equal(t, "3", got["BUILDKIT_MAX_PARALLELISM"])
}
