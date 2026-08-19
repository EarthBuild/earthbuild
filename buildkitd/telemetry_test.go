package buildkitd

import (
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	otelsemconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

func TestAddBuildkitTelemetryEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.test")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer token")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "cicd.pipeline.run.id=123,vcs.revision.id=abc")

	env := map[string]string{}
	addBuildkitTelemetryEnv(env, "earthly-buildkitd", "earthly", true)

	if got := env["OTEL_SERVICE_NAME"]; got != buildkitdOTELServiceName {
		t.Fatalf("OTEL_SERVICE_NAME = %q, want %q", got, buildkitdOTELServiceName)
	}

	// otlp is autoexport's default, and buildkitd's telemetry setup opts in on the
	// presence of the endpoint alone - so fabricating OTEL_METRICS_EXPORTER=otlp here
	// would only pin a default that is already the default.
	if got, ok := env["OTEL_METRICS_EXPORTER"]; ok {
		t.Fatalf("OTEL_METRICS_EXPORTER = %q, want unset", got)
	}

	if got := env["OTEL_EXPORTER_OTLP_ENDPOINT"]; got != "https://otel.example.test" {
		t.Fatalf("OTEL_EXPORTER_OTLP_ENDPOINT = %q", got)
	}

	if got := env["OTEL_EXPORTER_OTLP_HEADERS"]; got != "authorization=Bearer token" {
		t.Fatalf("OTEL_EXPORTER_OTLP_HEADERS = %q", got)
	}

	if got := env["OTEL_EXPORTER_OTLP_PROTOCOL"]; got != "http/protobuf" {
		t.Fatalf("OTEL_EXPORTER_OTLP_PROTOCOL = %q", got)
	}

	attrs := parseResourceAttrs(env["OTEL_RESOURCE_ATTRIBUTES"])
	wantAttrs := map[string]string{
		"cicd.pipeline.run.id":               "123",
		"vcs.revision.id":                    "abc",
		string(semconv.ProcessRole):          semconv.ProcessRoleBuildkitd.Value.AsString(),
		string(semconv.ProcessNesting):       semconv.ProcessNestingInner.Value.AsString(),
		string(otelsemconv.ContainerNameKey): "earthly-buildkitd",
		string(semconv.InstallationName):     "earthly",
	}

	for key, want := range wantAttrs {
		if got := attrs[key]; got != want {
			t.Fatalf("resource attr %s = %q, want %q", key, got, want)
		}
	}
}

// buildkitd inherits OTEL_RESOURCE_ATTRIBUTES from the outer earth process, so
// the keys it sets to describe itself collide with the ones already describing
// the CLI. Ours must win, and must appear exactly once - a duplicate key is
// resolved differently by different collectors, and losing the override would
// tag buildkitd's metrics as the CLI's, which is the whole point of the PR.
func TestAddBuildkitTelemetryEnvOverridesInheritedResourceAttributes(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.test")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", strings.Join([]string{
		string(semconv.ProcessRole) + "=" + semconv.ProcessRoleCLI.Value.AsString(),
		string(semconv.ProcessNesting) + "=" + semconv.ProcessNestingOuter.Value.AsString(),
		"cicd.pipeline.run.id=123",
	}, ","))

	env := map[string]string{}
	addBuildkitTelemetryEnv(env, "earthly-buildkitd", "earthly", true)

	raw := env["OTEL_RESOURCE_ATTRIBUTES"]
	for _, key := range []string{string(semconv.ProcessRole), string(semconv.ProcessNesting)} {
		if got := strings.Count(raw, key+"="); got != 1 {
			t.Fatalf("attr %s appears %d times in %q, want 1", key, got, raw)
		}
	}

	attrs := parseResourceAttrs(raw)
	wantAttrs := map[string]string{
		string(semconv.ProcessRole):    semconv.ProcessRoleBuildkitd.Value.AsString(),
		string(semconv.ProcessNesting): semconv.ProcessNestingInner.Value.AsString(),
		"cicd.pipeline.run.id":         "123",
	}

	for key, want := range wantAttrs {
		if got := attrs[key]; got != want {
			t.Fatalf("resource attr %s = %q, want %q", key, got, want)
		}
	}
}

// An explicit exporter choice is the user's, not ours: it must reach buildkitd
// verbatim, and is enough to enable telemetry on its own - prometheus, console and
// none all need no endpoint.
func TestAddBuildkitTelemetryEnvPropagatesExplicitMetricsExporter(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_METRICS_EXPORTER", "prometheus")

	env := map[string]string{}
	addBuildkitTelemetryEnv(env, "earthly-buildkitd", "earthly", false)

	if got := env["OTEL_METRICS_EXPORTER"]; got != "prometheus" {
		t.Fatalf("OTEL_METRICS_EXPORTER = %q, want prometheus", got)
	}

	if got := env["OTEL_SERVICE_NAME"]; got != buildkitdOTELServiceName {
		t.Fatalf("OTEL_SERVICE_NAME = %q, want %q", got, buildkitdOTELServiceName)
	}
}

//nolint:paralleltest // clearOTELEnv calls t.Setenv, which forbids t.Parallel.
func TestAddBuildkitTelemetryEnvDoesNothingWithoutMetricsExporter(t *testing.T) {
	clearOTELEnv(t)

	env := map[string]string{}
	addBuildkitTelemetryEnv(env, "earthly-buildkitd", "earthly", false)

	if len(env) != 0 {
		t.Fatalf("env = %#v, want empty", env)
	}
}

// clearOTELEnv unsets every OTEL_* var addBuildkitTelemetryEnv reads. Without it a
// test asserting the disabled path fails on any machine that exports an OTLP endpoint
// - which is precisely the setup this feature asks users to adopt.
func clearOTELEnv(t *testing.T) {
	t.Helper()

	for _, key := range slices.Concat(otelPassthroughEnvVars, []string{"OTEL_RESOURCE_ATTRIBUTES"}) {
		t.Setenv(key, "")
	}
}

func parseResourceAttrs(value string) map[string]string {
	attrs := map[string]string{}

	for part := range strings.SplitSeq(value, ",") {
		key, value, ok := strings.Cut(part, "=")
		if ok {
			attrs[key] = value
		}
	}

	return attrs
}
