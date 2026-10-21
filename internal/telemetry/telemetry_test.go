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
	otelsemconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

func TestOptIn(t *testing.T) {
	const (
		endpoint        = "https://otel.example.test"
		exporterNone    = "none"
		exporterOTLP    = "otlp"
		boolTrue        = "true"
		sdkDisabled     = "OTEL_SDK_DISABLED"
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
			env:  map[string]string{metricsExporter: exporterOTLP},
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
				metricsExporter: exporterNone,
				otlpEndpoint:    endpoint,
			},
			want: false,
		},
		{
			name: "exporter NONE overrides endpoint case-insensitively",
			env: map[string]string{
				metricsExporter: "NONE",
				otlpEndpoint:    endpoint,
			},
			want: false,
		},
		{
			name: "OTEL_SDK_DISABLED overrides exporter and endpoint",
			env: map[string]string{
				sdkDisabled:     boolTrue,
				metricsExporter: exporterOTLP,
				otlpEndpoint:    endpoint,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(sdkDisabled, "")
			t.Setenv(metricsExporter, "")
			t.Setenv(otlpEndpoint, "")
			t.Setenv(metricsEndpoint, "")

			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got := optIn(metricsExporter, metricsEndpoint)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.env[metricsExporter], os.Getenv(metricsExporter))
		})
	}
}

func TestEnabled(t *testing.T) {
	t.Parallel()

	const (
		endpoint        = "https://otel.example.test"
		exporterNone    = "none"
		exporterOTLP    = "otlp"
		boolTrue        = "true"
		sdkDisabled     = "OTEL_SDK_DISABLED"
		otlpEndpoint    = "OTEL_EXPORTER_OTLP_ENDPOINT"
		tracesExporter  = "OTEL_TRACES_EXPORTER"
		metricsExporter = "OTEL_METRICS_EXPORTER"
		logsExporter    = "OTEL_LOGS_EXPORTER"
		tracesEndpoint  = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
		metricsEndpoint = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
		logsEndpoint    = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
	)

	tests := []struct {
		env  map[string]string
		name string
		want bool
	}{
		{
			name: "nil map",
			env:  nil,
			want: false,
		},
		{
			name: "empty map",
			env:  map[string]string{},
			want: false,
		},
		{
			name: "metadata-only OTEL vars",
			env: map[string]string{
				"OTEL_RESOURCE_ATTRIBUTES": "cicd.pipeline.run.id=123",
				"OTEL_SERVICE_NAME":        "earth-cli",
				sdkDisabled:                "false",
			},
			want: false,
		},
		{
			name: "generic endpoint set",
			env:  map[string]string{otlpEndpoint: endpoint},
			want: true,
		},
		{
			name: "traces endpoint set",
			env:  map[string]string{tracesEndpoint: endpoint},
			want: true,
		},
		{
			name: "metrics endpoint set",
			env:  map[string]string{metricsEndpoint: endpoint},
			want: true,
		},
		{
			name: "logs endpoint set",
			env:  map[string]string{logsEndpoint: endpoint},
			want: true,
		},
		{
			name: "traces exporter set",
			env:  map[string]string{tracesExporter: exporterOTLP},
			want: true,
		},
		{
			name: "metrics exporter set",
			env:  map[string]string{metricsExporter: exporterOTLP},
			want: true,
		},
		{
			name: "logs exporter set",
			env:  map[string]string{logsExporter: exporterOTLP},
			want: true,
		},
		{
			name: "generic endpoint with two signal exporters none and one unset",
			env: map[string]string{
				otlpEndpoint:    endpoint,
				tracesExporter:  exporterNone,
				metricsExporter: exporterNone,
			},
			want: true,
		},
		{
			name: "generic endpoint with all three signal exporters none",
			env: map[string]string{
				otlpEndpoint:    endpoint,
				tracesExporter:  exporterNone,
				metricsExporter: "NONE",
				logsExporter:    exporterNone,
			},
			want: false,
		},
		{
			name: "OTEL_SDK_DISABLED overrides endpoint and exporter",
			env: map[string]string{
				sdkDisabled:    boolTrue,
				otlpEndpoint:   endpoint,
				tracesExporter: exporterOTLP,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, Enabled(tt.env))
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
		serviceName  string
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
			serviceName:  "other-service",
			want: []attribute.KeyValue{
				semconv.ProcessNestingOuter,
				attribute.String("cicd.pipeline.run.id", "123"),
			},
		},
		{
			name:         "partial OTEL_RESOURCE_ATTRIBUTES reports error and keeps valid attributes",
			resourceAttr: "bare,cicd.pipeline.run.id=123",
			want: []attribute.KeyValue{
				semconv.ProcessNestingOuter,
				attribute.String("cicd.pipeline.run.id", "123"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handledErr = nil

			t.Setenv("EARTH_WITH_DOCKER", tt.withDocker)
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", tt.resourceAttr)
			t.Setenv("OTEL_SERVICE_NAME", tt.serviceName)

			res, err := newOTelResource(t.Context())
			require.NoError(t, err)

			assert.Contains(t, res.Attributes(), otelsemconv.ServiceName("EarthBuild"))
			assert.NotContains(t, res.Attributes(), otelsemconv.ServiceName("other-service"))
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

func TestSetupSkipsResourceWhenNotOptedIn(t *testing.T) {
	const (
		endpoint     = "https://otel.example.test"
		exporterNone = "none"
		sdkDisabled  = "OTEL_SDK_DISABLED"
		otlpEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	)

	var handledErr error

	previousHandler := otel.GetErrorHandler()
	previousPropagator := otel.GetTextMapPropagator()

	t.Cleanup(func() {
		otel.SetErrorHandler(previousHandler)
		otel.SetTextMapPropagator(previousPropagator)
	})

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		handledErr = err
	}))

	otelEnvVars := []string{
		sdkDisabled,
		otlpEndpoint,
		"OTEL_TRACES_EXPORTER",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_METRICS_EXPORTER",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_LOGS_EXPORTER",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
	}

	tests := []struct {
		env  map[string]string
		name string
	}{
		{
			name: "no exporter or endpoint set",
		},
		{
			name: "OTEL_SDK_DISABLED is true",
			env: map[string]string{
				sdkDisabled:  "true",
				otlpEndpoint: endpoint,
			},
		},
		{
			name: "all signal exporters set to none",
			env: map[string]string{
				otlpEndpoint:            endpoint,
				"OTEL_TRACES_EXPORTER":  exporterNone,
				"OTEL_METRICS_EXPORTER": exporterNone,
				"OTEL_LOGS_EXPORTER":    exporterNone,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handledErr = nil

			for _, k := range otelEnvVars {
				t.Setenv(k, "")
			}

			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			t.Setenv("EARTH_WITH_DOCKER", "not-a-bool")
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "malformed-without-equals")

			shutdown, err := Setup(t.Context())
			require.NoError(t, err)
			require.NotNil(t, shutdown)
			require.NoError(t, shutdown(t.Context()))
			require.NoError(t, handledErr)
		})
	}
}
