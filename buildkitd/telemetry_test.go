package buildkitd

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildkitTelemetryEnv(t *testing.T) {
	t.Parallel()

	environ := []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://host.docker.internal:4317",
		"OTEL_EXPORTER_OTLP_HEADERS=x-token=secret",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_RESOURCE_ATTRIBUTES=service.version=1.2.3,deployment.environment.name=ci",
		"OTEL_SERVICE_NAME=earth-cli",
		"OTEL_EMPTY=",
		"NOT_OTEL_VAR=ignored",
	}

	got := buildkitTelemetryEnv("earthbuild-buildkitd", "earthbuild", false, environ)

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

func TestBuildkitTelemetryEnvEmptyWhenNoOTELEnvSet(t *testing.T) {
	t.Parallel()

	assert.Nil(t, buildkitTelemetryEnv("earthbuild-buildkitd", "earthbuild", false, nil))
	assert.Nil(t, buildkitTelemetryEnv(
		"earthbuild-buildkitd",
		"earthbuild",
		false,
		[]string{"NOT_OTEL_VAR=1", "OTEL_EMPTY="},
	))
}

// buildkitd inherits OTEL_RESOURCE_ATTRIBUTES from the outer earth process, so
// the keys it sets to describe itself collide with the ones already describing
// the CLI. Ours must win, and must appear exactly once - a duplicate key is
// resolved differently by different collectors, and losing the override would
// tag buildkitd's metrics as the CLI's, which is the whole point of the PR.
func TestBuildkitTelemetryEnvOverridesInheritedResourceAttributes(t *testing.T) {
	t.Parallel()

	environ := []string{
		"OTEL_RESOURCE_ATTRIBUTES=" + strings.Join([]string{
			string(semconv.ProcessRole) + "=" + semconv.ProcessRoleCLI.Value.AsString(),
			string(semconv.ProcessNesting) + "=" + semconv.ProcessNestingOuter.Value.AsString(),
			"cicd.pipeline.run.id=123",
		}, ","),
	}

	env := buildkitTelemetryEnv("earth-buildkitd", "earth", true, environ)

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

func TestAppendOTELResourceAttributes(t *testing.T) {
	t.Parallel()

	const (
		roleKey = "earth.process.role"
		role    = "buildkitd"
		want    = "cicd.pipeline.run.id=123,earth.process.role=buildkitd"
	)

	tests := []struct {
		attrs map[string]string
		name  string
		base  string
		want  string
	}{
		{
			// OTEL_RESOURCE_ATTRIBUTES is a comma-separated key=value list in which ","
			// and "=" MUST be percent-encoded, per the OTel resource SDK spec. The
			// consumer (sdk/resource.fromEnv) splits on "," and the first "=", then
			// url.PathUnescape's the value - so an unencoded separator in a value
			// silently invents attributes, and a bare "%" makes decoding fail outright.
			name:  "encodes separators",
			base:  "",
			attrs: map[string]string{"earth.installation.name": "a,b=c 100% d"},
			want:  "earth.installation.name=a%2Cb%3Dc 100%25 d",
		},
		{
			// Inherited entries are already encoded by whoever wrote them; re-encoding
			// would double the escapes and hand the collector a literal "%2C".
			name:  "leaves inherited encoding alone",
			base:  "vcs.revision.id=a%2Cb",
			attrs: map[string]string{"earth.target": "+build"},
			want:  "vcs.revision.id=a%2Cb,earth.target=+build",
		},
		{
			name:  "missing key",
			base:  "=missing-key,cicd.pipeline.run.id=123",
			attrs: map[string]string{roleKey: role},
			want:  want,
		},
		{
			name:  "whitespace key",
			base:  "   =missing-key,cicd.pipeline.run.id=123",
			attrs: map[string]string{roleKey: role},
			want:  want,
		},
		{
			name:  "missing value",
			base:  "missing-value= ,cicd.pipeline.run.id=123",
			attrs: map[string]string{roleKey: role},
			want:  want,
		},
		{
			name:  "missing equals",
			base:  "bare,cicd.pipeline.run.id=123",
			attrs: map[string]string{roleKey: role},
			want:  want,
		},
		{
			name: "empty override strips inherited key",
			base: "earth.installation.name=inherited,cicd.pipeline.run.id=123",
			attrs: map[string]string{
				"earth.installation.name": "",
				roleKey:                   role,
			},
			want: want,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := appendOTELResourceAttributes(tt.base, tt.attrs)
			assert.Equal(t, tt.want, got)
		})
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
