package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log/global"
	otelsemconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	endpoint        = "https://otel.example.test"
	exporterNone    = "none"
	exporterOTLP    = "otlp"
	boolTrue        = "true"
	withDockerEnv   = "EARTH_WITH_DOCKER"
	pipelineRunAttr = "cicd.pipeline.run.id=123"
)

func TestEnabled(t *testing.T) {
	t.Parallel()

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
				resourceAttrEnv: pipelineRunAttr,
				serviceNameEnv:  "earth-cli",
				sdkDisabledEnv:  "false",
			},
			want: false,
		},
		{
			name: "generic endpoint set",
			env:  map[string]string{otlpEndpointEnv: endpoint},
			want: true,
		},
		{
			name: "traces endpoint set",
			env:  map[string]string{tracesEndpointEnv: endpoint},
			want: true,
		},
		{
			name: "metrics endpoint set",
			env:  map[string]string{metricsEndpointEnv: endpoint},
			want: true,
		},
		{
			name: "logs endpoint set",
			env:  map[string]string{logsEndpointEnv: endpoint},
			want: true,
		},
		{
			name: "traces exporter set",
			env:  map[string]string{tracesExporterEnv: exporterOTLP},
			want: true,
		},
		{
			name: "metrics exporter set",
			env:  map[string]string{metricsExporterEnv: exporterOTLP},
			want: true,
		},
		{
			name: "logs exporter set",
			env:  map[string]string{logsExporterEnv: exporterOTLP},
			want: true,
		},
		{
			name: "generic endpoint with two signal exporters none and one unset",
			env: map[string]string{
				otlpEndpointEnv:    endpoint,
				tracesExporterEnv:  exporterNone,
				metricsExporterEnv: exporterNone,
			},
			want: true,
		},
		{
			name: "generic endpoint with all three signal exporters none",
			env: map[string]string{
				otlpEndpointEnv:    endpoint,
				tracesExporterEnv:  exporterNone,
				metricsExporterEnv: "NONE",
				logsExporterEnv:    exporterNone,
			},
			want: false,
		},
		{
			name: "OTEL_SDK_DISABLED overrides endpoint and exporter",
			env: map[string]string{
				sdkDisabledEnv:    boolTrue,
				otlpEndpointEnv:   endpoint,
				tracesExporterEnv: exporterOTLP,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, enabled(tt.env))
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
			withDocker: boolTrue,
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

			t.Setenv(withDockerEnv, tt.withDocker)
			t.Setenv(resourceAttrEnv, tt.resourceAttr)
			t.Setenv(serviceNameEnv, tt.serviceName)

			res, err := newOTelResource(t.Context())
			require.NoError(t, err)

			assert.Contains(t, res.Attributes(), otelsemconv.ServiceName(cliServiceName))
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
		sdkDisabledEnv,
		otlpEndpointEnv,
		tracesExporterEnv,
		tracesEndpointEnv,
		metricsExporterEnv,
		metricsEndpointEnv,
		logsExporterEnv,
		logsEndpointEnv,
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
				sdkDisabledEnv:  boolTrue,
				otlpEndpointEnv: endpoint,
			},
		},
		{
			name: "all signal exporters set to none",
			env: map[string]string{
				otlpEndpointEnv:    endpoint,
				tracesExporterEnv:  exporterNone,
				metricsExporterEnv: exporterNone,
				logsExporterEnv:    exporterNone,
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

			t.Setenv(withDockerEnv, "not-a-bool")
			t.Setenv(resourceAttrEnv, "malformed-without-equals")

			shutdown, err := Setup(t.Context())
			require.NoError(t, err)
			require.NotNil(t, shutdown)
			assert.Equal(t, previousPropagator, otel.GetTextMapPropagator())
			require.NoError(t, shutdown(t.Context()))
			require.NoError(t, handledErr)
		})
	}
}

func TestSetupOptedInLifecycle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	prevTracerProvider := otel.GetTracerProvider()
	prevMeterProvider := otel.GetMeterProvider()
	prevLoggerProvider := global.GetLoggerProvider()
	prevPropagator := otel.GetTextMapPropagator()
	prevErrorHandler := otel.GetErrorHandler()
	prevTransport := http.DefaultClient.Transport

	t.Cleanup(func() {
		otel.SetTracerProvider(prevTracerProvider)
		otel.SetMeterProvider(prevMeterProvider)
		global.SetLoggerProvider(prevLoggerProvider)
		otel.SetTextMapPropagator(prevPropagator)
		otel.SetErrorHandler(prevErrorHandler)

		http.DefaultClient.Transport = prevTransport
	})

	for _, k := range []string{
		sdkDisabledEnv,
		tracesExporterEnv,
		tracesEndpointEnv,
		metricsExporterEnv,
		metricsEndpointEnv,
		logsExporterEnv,
		logsEndpointEnv,
		resourceAttrEnv,
		serviceNameEnv,
		withDockerEnv,
	} {
		t.Setenv(k, "")
	}

	t.Setenv(otlpEndpointEnv, srv.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

	shutdown, err := Setup(t.Context())
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	assert.NotEqual(t, prevTracerProvider, otel.GetTracerProvider())
	assert.NotEqual(t, prevMeterProvider, otel.GetMeterProvider())
	assert.NotEqual(t, prevLoggerProvider, global.GetLoggerProvider())
	assert.NotEqual(t, prevPropagator, otel.GetTextMapPropagator())
	assert.NotEqual(t, prevTransport, http.DefaultClient.Transport)

	require.NoError(t, shutdown(t.Context()))
	assert.Equal(t, prevTracerProvider, otel.GetTracerProvider())
	assert.Equal(t, prevMeterProvider, otel.GetMeterProvider())
	assert.Equal(t, prevLoggerProvider, global.GetLoggerProvider())
	assert.Equal(t, prevPropagator, otel.GetTextMapPropagator())
	assert.Equal(t, prevTransport, http.DefaultClient.Transport)
}
