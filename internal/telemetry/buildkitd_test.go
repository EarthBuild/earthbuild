package telemetry

import (
	"net/url"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func TestBuildkitdEnv(t *testing.T) {
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

	got := BuildkitdEnv("earthbuild-buildkitd", "earthbuild", false, environ)

	want := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://host.docker.internal:4317",
		"OTEL_EXPORTER_OTLP_HEADERS":  "x-token=secret",
		"OTEL_TRACES_EXPORTER":        "otlp",
		"OTEL_SERVICE_NAME":           buildkitdServiceName,
		"OTEL_RESOURCE_ATTRIBUTES": "service.version=1.2.3," +
			"deployment.environment.name=ci," +
			"container.name=earthbuild-buildkitd," +
			"earth.installation.name=earthbuild," +
			"earth.process.nesting=outer," +
			"earth.process.role=buildkitd",
	}

	require.Equal(t, want, got)
}

func TestBuildkitdEnvEmptyWhenNoOTELEnvSet(t *testing.T) {
	t.Parallel()

	const endpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT=http://otel:4317"

	tests := []struct {
		name    string
		environ []string
	}{
		{
			name:    "nil environ",
			environ: nil,
		},
		{
			name:    "non-OTEL and empty or whitespace OTEL vars",
			environ: []string{"NOT_OTEL_VAR=1", "OTEL_EMPTY=", "OTEL_WHITESPACE=   "},
		},
		{
			name:    "later empty value clears earlier OTEL var",
			environ: []string{endpointEnv, "OTEL_EXPORTER_OTLP_ENDPOINT="},
		},
		{
			name:    "OTEL_SDK_DISABLED is true",
			environ: []string{endpointEnv, "OTEL_SDK_DISABLED=true"},
		},
		{
			name: "metadata-only OTEL vars without exporter or endpoint",
			environ: []string{
				"OTEL_RESOURCE_ATTRIBUTES=cicd.pipeline.run.id=123",
				"OTEL_SERVICE_NAME=earth-cli",
				"OTEL_SDK_DISABLED=false",
			},
		},
		{
			name: "all signal exporters set to none",
			environ: []string{
				endpointEnv,
				"OTEL_TRACES_EXPORTER=none",
				"OTEL_METRICS_EXPORTER=NONE",
				"OTEL_LOGS_EXPORTER=none",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Nil(t, BuildkitdEnv("earthbuild-buildkitd", "earthbuild", false, tt.environ))
		})
	}
}

// buildkitd inherits OTEL_RESOURCE_ATTRIBUTES from the outer earth process, so
// the keys it sets to describe itself collide with the ones already describing
// the CLI. Ours must win, and must appear exactly once - a duplicate key is
// resolved differently by different collectors, and losing the override would
// tag buildkitd's metrics as the CLI's, which is the whole point of the PR.
func TestBuildkitdEnvOverridesInheritedResourceAttributes(t *testing.T) {
	t.Parallel()

	environ := []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://otel:4317",
		"OTEL_RESOURCE_ATTRIBUTES=earth.process.role=cli,earth.process.nesting=outer,cicd.pipeline.run.id=123",
	}

	env := BuildkitdEnv("earth-buildkitd", "earth", true, environ)

	raw := env[resourceAttrEnv]
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
		wantUnescaped map[string]string
		name          string
		base          string
		want          string
		attrs         []attribute.KeyValue
	}{
		{
			// OTEL_RESOURCE_ATTRIBUTES is a comma-separated key=value list in which ","
			// and "=" MUST be percent-encoded, per the OTel resource SDK spec. The
			// consumer (sdk/resource.fromEnv) splits on "," and the first "=", then
			// url.PathUnescape's the value - so an unencoded separator in a value
			// silently invents attributes, and a bare "%" makes decoding fail outright.
			name:          "encodes separators",
			base:          "",
			attrs:         []attribute.KeyValue{attribute.String("earth.installation.name", "a,b=c 100% d")},
			want:          "earth.installation.name=a%2Cb%3Dc 100%25 d",
			wantUnescaped: map[string]string{"earth.installation.name": "a,b=c 100% d"},
		},
		{
			// Inherited entries are already encoded by whoever wrote them; re-encoding
			// would double the escapes and hand the collector a literal "%2C".
			name:          "leaves inherited encoding alone",
			base:          "vcs.revision.id=a%2Cb",
			attrs:         []attribute.KeyValue{attribute.String("earth.target", "+build")},
			want:          "vcs.revision.id=a%2Cb,earth.target=+build",
			wantUnescaped: map[string]string{"vcs.revision.id": "a,b"},
		},
		{
			name:  "missing key",
			base:  "=missing-key,cicd.pipeline.run.id=123",
			attrs: []attribute.KeyValue{attribute.String(roleKey, role)},
			want:  want,
		},
		{
			name:  "whitespace key",
			base:  "   =missing-key,cicd.pipeline.run.id=123",
			attrs: []attribute.KeyValue{attribute.String(roleKey, role)},
			want:  want,
		},
		{
			name:  "missing value",
			base:  "missing-value= ,cicd.pipeline.run.id=123",
			attrs: []attribute.KeyValue{attribute.String(roleKey, role)},
			want:  want,
		},
		{
			name:  "missing equals",
			base:  "bare,cicd.pipeline.run.id=123",
			attrs: []attribute.KeyValue{attribute.String(roleKey, role)},
			want:  want,
		},
		{
			name: "empty override strips inherited key",
			base: "earth.installation.name=inherited,cicd.pipeline.run.id=123",
			attrs: []attribute.KeyValue{
				attribute.String("earth.installation.name", ""),
				attribute.String(roleKey, role),
			},
			want: want,
		},
		{
			name:  "trims whitespace around base key and value",
			base:  " cicd.pipeline.run.id = 123 ",
			attrs: []attribute.KeyValue{attribute.String(roleKey, role)},
			want:  want,
		},
		{
			name:  "non-string attribute value",
			base:  pipelineRunAttr,
			attrs: []attribute.KeyValue{attribute.Int("earth.retry.count", 2)},
			want:  "cicd.pipeline.run.id=123,earth.retry.count=2",
		},
		{
			name:  "deduplicates repeated keys in base",
			base:  "a=1,cicd.pipeline.run.id=123,a=2",
			attrs: []attribute.KeyValue{semconv.ProcessRoleBuildkitd},
			want:  "cicd.pipeline.run.id=123,a=2,earth.process.role=buildkitd",
		},
		{
			name:  "empty value in base clears earlier key in base",
			base:  "a=1,cicd.pipeline.run.id=123,a=",
			attrs: []attribute.KeyValue{semconv.ProcessRoleBuildkitd},
			want:  want,
		},
		{
			name:  "deduplicates repeated keys in attrs",
			base:  pipelineRunAttr,
			attrs: []attribute.KeyValue{semconv.ProcessRoleCLI, semconv.ProcessRoleBuildkitd},
			want:  want,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := appendOTELResourceAttributes(tt.base, tt.attrs...)
			assert.Equal(t, tt.want, got)

			for key, rawVal := range parseResourceAttrs(got) {
				unescaped, err := url.PathUnescape(rawVal)
				require.NoError(t, err)

				if wantVal, ok := tt.wantUnescaped[key]; ok {
					assert.Equal(t, wantVal, unescaped)
				}
			}
		})
	}
}

func parseResourceAttrs(value string) map[string]string {
	attrs := map[string]string{}

	for part := range strings.SplitSeq(value, ",") {
		key, val, ok := strings.Cut(part, "=")
		if ok {
			attrs[strings.TrimSpace(key)] = strings.TrimSpace(val)
		}
	}

	return attrs
}
