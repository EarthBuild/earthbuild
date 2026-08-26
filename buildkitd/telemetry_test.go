package buildkitd

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	otelsemconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

func TestBuildkitTelemetryEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.test")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer token")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "cicd.pipeline.run.id=123,vcs.revision.id=abc")

	env := buildkitTelemetryEnv("earthly-buildkitd", "earthly", true)

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
func TestBuildkitTelemetryEnvOverridesInheritedResourceAttributes(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.test")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", strings.Join([]string{
		string(semconv.ProcessRole) + "=" + semconv.ProcessRoleCLI.Value.AsString(),
		string(semconv.ProcessNesting) + "=" + semconv.ProcessNestingOuter.Value.AsString(),
		"cicd.pipeline.run.id=123",
	}, ","))

	env := buildkitTelemetryEnv("earthly-buildkitd", "earthly", true)

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
func TestBuildkitTelemetryEnvPropagatesExplicitMetricsExporter(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_METRICS_EXPORTER", "prometheus")

	env := buildkitTelemetryEnv("earthly-buildkitd", "earthly", false)

	if got := env["OTEL_METRICS_EXPORTER"]; got != "prometheus" {
		t.Fatalf("OTEL_METRICS_EXPORTER = %q, want prometheus", got)
	}

	if got := env["OTEL_SERVICE_NAME"]; got != buildkitdOTELServiceName {
		t.Fatalf("OTEL_SERVICE_NAME = %q, want %q", got, buildkitdOTELServiceName)
	}
}

//nolint:paralleltest // clearOTELEnv calls t.Setenv, which forbids t.Parallel.
func TestBuildkitTelemetryEnvReturnsNilWithoutMetricsExporter(t *testing.T) {
	clearOTELEnv(t)

	env := buildkitTelemetryEnv("earthly-buildkitd", "earthly", false)

	if env != nil {
		t.Fatalf("env = %#v, want nil", env)
	}
}

// clearOTELEnv unsets every OTEL_* var buildkitTelemetryEnv reads. Without it a
// test asserting the disabled path fails on any machine that exports an OTLP endpoint
// - which is precisely the setup this feature asks users to adopt.
func clearOTELEnv(t *testing.T) {
	t.Helper()

	for _, key := range slices.Concat(otelPassthroughEnvVars, []string{"OTEL_RESOURCE_ATTRIBUTES"}) {
		t.Setenv(key, "")
	}
}

// OTEL_RESOURCE_ATTRIBUTES is a comma-separated key=value list in which "," and "="
// MUST be percent-encoded, per
// https://opentelemetry.io/docs/specs/otel/resource/sdk/#specifying-resource-information-via-an-environment-variable.
// The consumer (sdk/resource.fromEnv) splits on "," and the first "=", then
// url.PathUnescape's the value - so an unencoded separator in a value silently
// invents attributes, and a bare "%" makes decoding fail outright.
func TestAppendOTELResourceAttributesEncodesSeparators(t *testing.T) {
	t.Parallel()

	const nasty = "a,b=c 100% d"

	got := appendOTELResourceAttributes("", map[string]string{"earth.installation.name": nasty})

	if strings.Count(got, ",") != 0 {
		t.Fatalf("got %q, want no bare separator", got)
	}

	key, value, ok := strings.Cut(got, "=")
	if !ok || key != "earth.installation.name" {
		t.Fatalf("got %q, want key earth.installation.name", got)
	}

	decoded, err := url.PathUnescape(value)
	if err != nil {
		t.Fatalf("PathUnescape(%q): %v", value, err)
	}

	if decoded != nasty {
		t.Fatalf("decoded = %q, want %q", decoded, nasty)
	}
}

// Inherited entries are already encoded by whoever wrote them; re-encoding would
// double the escapes and hand the collector a literal "%2C".
func TestAppendOTELResourceAttributesLeavesInheritedEncodingAlone(t *testing.T) {
	t.Parallel()

	got := appendOTELResourceAttributes("vcs.revision.id=a%2Cb", map[string]string{"earth.target": "+build"})

	if want := "vcs.revision.id=a%2Cb,earth.target=+build"; got != want {
		t.Fatalf("got %q, want %q", got, want)
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
