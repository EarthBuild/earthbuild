package telemetry

import (
	"context"
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
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

	err := os.Unsetenv("OTEL_METRICS_EXPORTER")
	if err != nil {
		t.Fatalf("unset OTEL_METRICS_EXPORTER: %v", err)
	}

	err = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if err != nil {
		t.Fatalf("unset OTEL_EXPORTER_OTLP_ENDPOINT: %v", err)
	}

	got, err := optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT")
	if err != nil {
		t.Fatalf("optIn: %v", err)
	}

	if got {
		t.Fatal("optIn = true with nothing set, want false")
	}

	if v := os.Getenv("OTEL_METRICS_EXPORTER"); v != "none" {
		t.Fatalf("OTEL_METRICS_EXPORTER = %q after opting out, want %q", v, "none")
	}
}

// Any one of the keys opts in, not just the exporter - an endpoint alone is how CI
// configures earth, and autoexport picks otlp from there.
func TestOptInOnAnyKey(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.test")

	got, err := optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT")
	if err != nil {
		t.Fatalf("optIn: %v", err)
	}

	if !got {
		t.Fatal("optIn = false with only the endpoint set, want true")
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

	sc := trace.SpanContextFromContext(WithTraceparent(context.Background()))

	if got := sc.TraceID().String(); got != traceID {
		t.Fatalf("trace ID = %q, want %q", got, traceID)
	}

	if got := sc.SpanID().String(); got != spanID {
		t.Fatalf("span ID = %q, want %q", got, spanID)
	}

	if !sc.IsRemote() {
		t.Fatal("span context is not remote, want remote")
	}
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
			if err != nil {
				t.Fatalf("newOTelResource: %v", err)
			}

			assertAttribute(t, res.Attributes(), semconv.ProcessRoleCLI)
			assertAttribute(t, res.Attributes(), tt.want)
		})
	}
}

func assertAttribute(t *testing.T, attrs []attribute.KeyValue, want attribute.KeyValue) {
	t.Helper()

	for _, attr := range attrs {
		if attr.Key != want.Key {
			continue
		}

		if attr.Value != want.Value {
			t.Fatalf("%s = %v, want %v", want.Key, attr.Value.AsString(), want.Value.AsString())
		}

		return
	}

	t.Fatalf("%s missing from resource, want %v", want.Key, want.Value.AsString())
}
