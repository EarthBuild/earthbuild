package buildkitd

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildkitTelemetryEnv(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://host.docker.internal:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-token=secret")
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=1.2.3,deployment.environment.name=ci")
	t.Setenv("OTEL_SERVICE_NAME", "earth-cli")
	t.Setenv("NOT_OTEL_VAR", "ignored")

	got := buildkitTelemetryEnv("earthbuild-buildkitd", "earthbuild", false)

	want := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://host.docker.internal:4317",
		"OTEL_EXPORTER_OTLP_HEADERS":  "x-token=secret",
		"OTEL_TRACES_EXPORTER":        "otlp",
		"OTEL_SERVICE_NAME":           buildkitdOTELServiceName,
		"OTEL_RESOURCE_ATTRIBUTES": "service.version=1.2.3," +
			"deployment.environment.name=ci," +
			"container.name=earthbuild-buildkitd," +
			"earth.installation.name=earthbuild," +
			"earth.process.nesting=outer," +
			"earth.process.role=buildkitd",
	}

	require.Equal(t, want, got)
}

//nolint:paralleltest // clearOTELEnv calls t.Setenv, which forbids t.Parallel.
func TestBuildkitTelemetryEnvEmptyWhenNoOTELEnvSet(t *testing.T) {
	clearOTELEnv(t)

	assert.Nil(t, buildkitTelemetryEnv("earthbuild-buildkitd", "earthbuild", false))
}

// buildkitd inherits OTEL_RESOURCE_ATTRIBUTES from the outer earth process, so
// the keys it sets to describe itself collide with the ones already describing
// the CLI. Ours must win, and must appear exactly once - a duplicate key is
// resolved differently by different collectors, and losing the override would
// tag buildkitd's metrics as the CLI's, which is the whole point of the PR.
func TestBuildkitTelemetryEnvOverridesInheritedResourceAttributes(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", strings.Join([]string{
		string(semconv.ProcessRole) + "=" + semconv.ProcessRoleCLI.Value.AsString(),
		string(semconv.ProcessNesting) + "=" + semconv.ProcessNestingOuter.Value.AsString(),
		"cicd.pipeline.run.id=123",
	}, ","))

	env := buildkitTelemetryEnv("earth-buildkitd", "earth", true)

	raw := env["OTEL_RESOURCE_ATTRIBUTES"]
	for _, key := range []string{string(semconv.ProcessRole), string(semconv.ProcessNesting)} {
		assert.Equal(t, 1, strings.Count(raw, key+"="), "attr %s in %q", key, raw)
	}

	attrs := parseResourceAttrs(raw)
	wantAttrs := map[string]string{
		string(semconv.ProcessRole):    semconv.ProcessRoleBuildkitd.Value.AsString(),
		string(semconv.ProcessNesting): semconv.ProcessNestingInner.Value.AsString(),
		"cicd.pipeline.run.id":         "123",
	}

	for key, want := range wantAttrs {
		assert.Equal(t, want, attrs[key], "resource attr %s", key)
	}
}

// clearOTELEnv unsets every OTEL_* var buildkitTelemetryEnv reads. Without it a
// test asserting the disabled path fails on any machine that exports an OTLP endpoint
// - which is precisely the setup this feature asks users to adopt.
func clearOTELEnv(t *testing.T) {
	t.Helper()

	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "OTEL_") {
			continue
		}

		key, _, ok := strings.Cut(entry, "=")
		if ok {
			t.Setenv(key, "")
		}
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
	assert.NotContains(t, got, ",")

	key, value, ok := strings.Cut(got, "=")
	require.True(t, ok)
	assert.Equal(t, "earth.installation.name", key)

	decoded, err := url.PathUnescape(value)
	require.NoError(t, err)
	assert.Equal(t, nasty, decoded)
}

// Inherited entries are already encoded by whoever wrote them; re-encoding would
// double the escapes and hand the collector a literal "%2C".
func TestAppendOTELResourceAttributesLeavesInheritedEncodingAlone(t *testing.T) {
	t.Parallel()

	got := appendOTELResourceAttributes("vcs.revision.id=a%2Cb", map[string]string{"earth.target": "+build"})
	assert.Equal(t, "vcs.revision.id=a%2Cb,earth.target=+build", got)
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
