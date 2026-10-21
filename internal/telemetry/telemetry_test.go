package telemetry

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestOptIn(t *testing.T) {
	const (
		endpoint        = "https://otel.example.test"
		metricsExporter = "OTEL_METRICS_EXPORTER"
		otlpEndpoint    = "OTEL_EXPORTER_OTLP_ENDPOINT"
		metricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	)

	tests := []struct {
		env  map[string]string
		name string
		want bool
	}{
		{
			name: "nothing set",
			want: false,
		},
		{
			name: "exporter set",
			env:  map[string]string{metricsExporter: "otlp"},
			want: true,
		},
		{
			name: "generic endpoint set",
			env:  map[string]string{otlpEndpoint: endpoint},
			want: true,
		},
		{
			name: "signal endpoint set",
			env:  map[string]string{metricsEndpoint: endpoint},
			want: true,
		},
		{
			name: "exporter none overrides endpoint",
			env: map[string]string{
				metricsExporter: "none",
				otlpEndpoint:    endpoint,
			},
			want: false,
		},
		{
			name: "OTEL_SDK_DISABLED overrides exporter and endpoint",
			env: map[string]string{
				"OTEL_SDK_DISABLED": "true",
				metricsExporter:     "otlp",
				otlpEndpoint:        endpoint,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_SDK_DISABLED", "")
			t.Setenv(metricsExporter, "")
			t.Setenv(otlpEndpoint, "")
			t.Setenv(metricsEndpoint, "")

			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got := optIn(
				metricsExporter,
				otlpEndpoint,
				metricsEndpoint,
			)
			assert.Equal(t, tt.want, got)

			gotReversed := optIn(
				otlpEndpoint,
				metricsEndpoint,
				metricsExporter,
			)
			assert.Equal(t, tt.want, gotReversed)
			assert.Equal(t, tt.env[metricsExporter], os.Getenv(metricsExporter))
		})
	}
}

// WithTraceparent is the CI-to-earth trace link: a build step exports TRACEPARENT and
// earth's spans have to land under the pipeline's trace rather than starting their own.
func TestWithTraceparentAdoptsTheCallersTrace(t *testing.T) {
	const (
		traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
		spanID  = "00f067aa0ba902b7"
	)

	t.Setenv("TRACEPARENT", "00-"+traceID+"-"+spanID+"-01")

	previous := otel.GetTextMapPropagator()

	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	otel.SetTextMapPropagator(propagation.TraceContext{})

	sc := trace.SpanContextFromContext(WithTraceparent(t.Context()))

	assert.Equal(t, traceID, sc.TraceID().String())
	assert.Equal(t, spanID, sc.SpanID().String())
	assert.True(t, sc.IsRemote())
}

// otelruntime's instruments carry resource attributes and nothing else, so the
// resource is the only place earth's own identity can reach its metrics.
func TestNewOTelResourceCarriesProcessIdentity(t *testing.T) {
	var handledErr error

	previousHandler := otel.GetErrorHandler()

	t.Cleanup(func() { otel.SetErrorHandler(previousHandler) })

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		handledErr = err
	}))

	tests := []struct {
		name         string
		withDocker   string
		resourceAttr string
		want         []attribute.KeyValue
		wantErr      bool
	}{
		{
			name: "outer build",
			want: []attribute.KeyValue{semconv.ProcessNestingOuter},
		},
		{
			name:       "inside WITH DOCKER",
			withDocker: "true",
			want:       []attribute.KeyValue{semconv.ProcessNestingInner},
		},
		{
			name:       "invalid EARTH_WITH_DOCKER falls back to outer",
			withDocker: "not-a-bool",
			want:       []attribute.KeyValue{semconv.ProcessNestingOuter},
			wantErr:    true,
		},
		{
			name:         "overrides inherited OTEL_RESOURCE_ATTRIBUTES",
			resourceAttr: "cicd.pipeline.run.id=123,earth.process.role=buildkitd,earth.process.nesting=inner",
			want: []attribute.KeyValue{
				semconv.ProcessNestingOuter,
				attribute.String("cicd.pipeline.run.id", "123"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handledErr = nil

			t.Setenv("EARTH_WITH_DOCKER", tt.withDocker)
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", tt.resourceAttr)

			res, err := newOTelResource(t.Context())
			require.NoError(t, err)

			assert.Contains(t, res.Attributes(), semconv.ProcessRoleCLI)
			assert.NotContains(t, res.Attributes(), semconv.ProcessRoleBuildkitd)

			for _, wantAttr := range tt.want {
				assert.Contains(t, res.Attributes(), wantAttr)
			}

			if tt.wantErr {
				require.Error(t, handledErr)
			} else {
				require.NoError(t, handledErr)
			}
		})
	}
}
