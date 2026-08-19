package buildkitd

import (
	"maps"
	"testing"
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
				"EARTHLY_ADDITIONAL_BUILDKIT_CONFIG": "[worker]",
				"IP_TABLES":                          "nf_tables",
				"CNI_MTU":                            "1400",
				"CACHE_SIZE_MB":                      "100",
				"CACHE_SIZE_PCT":                     "50",
				"CACHE_KEEP_DURATION":                "300",
				"BUILDKIT_PPROF_ENABLED":             enabled,
				"EARTHLY_RESET_TMP_DIR":              enabled,
			}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearOTELEnv(t)

			got := buildkitEnv(test.settings, "earthly-buildkitd", "earthly", false, test.reset)

			if !maps.Equal(got, test.want) {
				t.Fatalf("buildkitEnv() = %#v, want %#v", got, test.want)
			}
		})
	}
}

// The telemetry env has to survive the merge into the rest of the environment.
func TestBuildkitEnvIncludesTelemetry(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.test")

	got := buildkitEnv(Settings{}, "earthly-buildkitd", "earthly", false, false)

	if got["OTEL_SERVICE_NAME"] != buildkitdOTELServiceName {
		t.Fatalf("OTEL_SERVICE_NAME = %q, want %q", got["OTEL_SERVICE_NAME"], buildkitdOTELServiceName)
	}

	if got["BUILDKIT_MAX_PARALLELISM"] != "0" {
		t.Fatalf("BUILDKIT_MAX_PARALLELISM = %q, want 0 - the merge dropped the base env", got["BUILDKIT_MAX_PARALLELISM"])
	}
}
