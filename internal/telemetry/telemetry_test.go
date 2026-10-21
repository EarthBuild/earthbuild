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

// optIn is the switch the whole package hangs off, and its opt-out is a side effect
// rather than a return value: autoexport defaults to otlp, so declining telemetry
// means writing OTEL_..._EXPORTER=none before the SDK reads it. Lose that and an
// unconfigured earth starts trying to export.
func TestOptInPinsExporterToNoneWhenNothingIsSet(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	got, err := optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT")
	require.NoError(t, err)
	assert.False(t, got)
	assert.Equal(t, "none", os.Getenv("OTEL_METRICS_EXPORTER"))
}

// Any one of the keys opts in, not just the exporter - an endpoint alone is how CI
// configures earth, and autoexport picks otlp from there.
func TestOptInOnAnyKey(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.test")

	got, err := optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT")
	require.NoError(t, err)
	assert.True(t, got)

	t.Setenv("OTEL_METRICS_EXPORTER", "none")

	got, err = optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT")
	require.NoError(t, err)
	assert.False(t, got)
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
	tests := []struct {
		want       attribute.KeyValue
		name       string
		withDocker string
	}{
		{name: "outer build", withDocker: "", want: semconv.ProcessNestingOuter},
		{name: "inside WITH DOCKER", withDocker: "true", want: semconv.ProcessNestingInner},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("EARTH_WITH_DOCKER", tt.withDocker)

			res, err := newOTelResource(t.Context())
			require.NoError(t, err)

			assert.Contains(t, res.Attributes(), semconv.ProcessRoleCLI)
			assert.Contains(t, res.Attributes(), tt.want)
		})
	}
}
