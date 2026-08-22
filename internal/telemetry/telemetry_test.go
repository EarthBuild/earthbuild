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

func TestEarthbuildTargetFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "no target",
			args: []string{"earth", "--version"},
			want: "",
		},
		{
			name: "no arguments at all",
			args: []string{"earth"},
			want: "",
		},
		{
			name: "bare target",
			args: []string{"earth", "+build"},
			want: "+build",
		},
		{
			name: "target after flags",
			args: []string{"earth", "--verbose", "--no-cache", "+build"},
			want: "+build",
		},
		{
			name: "path-qualified target",
			args: []string{"earth", "./subdir+build"},
			want: "./subdir+build",
		},
		{
			name: "remote target",
			args: []string{"earth", "github.com/EarthBuild/earthbuild+build"},
			want: "github.com/EarthBuild/earthbuild+build",
		},
		{
			name: "first of several targets",
			args: []string{"earth", "+build", "+test"},
			want: "+build",
		},
		{
			// Known limit of the heuristic, pinned rather than hidden: a target may be
			// path- or repo-qualified, so it cannot be recognised by a leading "+", and
			// without the flag set there is no way to tell a flag's value from a target.
			// The attribute is best-effort labelling, so a rare mislabel beats parsing
			// the CLI twice - but it should not be discovered by surprise.
			name: "mistakes a separate flag value containing + for the target",
			args: []string{"earth", "--build-arg", "VERSION=1.0+beta", "+build"},
			want: "VERSION=1.0+beta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := earthbuildTargetFromArgs(tt.args); got != tt.want {
				t.Fatalf("earthbuildTargetFromArgs(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestOptInRejectsNonExporterFirstKey(t *testing.T) {
	_, err := optIn("OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_METRICS_EXPORTER")
	if err == nil {
		t.Fatal("optIn accepted a first key that is not OTEL_..._EXPORTER, want error")
	}
}

func TestOptInOnExporter(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	unsetEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT")

	got, err := optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT")
	if err != nil {
		t.Fatalf("optIn: %v", err)
	}

	if !got {
		t.Fatal("optIn = false with OTEL_METRICS_EXPORTER set, want true")
	}
}

func TestOptInOnAnySecondaryKey(t *testing.T) {
	unsetEnv(t, "OTEL_METRICS_EXPORTER")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.test")

	got, err := optIn("OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT")
	if err != nil {
		t.Fatalf("optIn: %v", err)
	}

	if !got {
		t.Fatal("optIn = false with only the endpoint set, want true")
	}
}

// The opt-out is a side effect, not just a return value: autoexport defaults to otlp,
// so declining telemetry means actively pinning the exporter to "none" before the SDK
// reads it. Drop that and an un-configured earth starts trying to export.
func TestOptInPinsExporterToNoneWhenNothingIsSet(t *testing.T) {
	unsetEnv(t, "OTEL_METRICS_EXPORTER")
	unsetEnv(t, "OTEL_EXPORTER_OTLP_ENDPOINT")

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

func TestWithTraceparentIsANoopWhenUnset(t *testing.T) {
	setTraceContextPropagator(t)
	unsetEnv(t, "TRACEPARENT")

	ctx := WithTraceparent(context.Background())

	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		t.Fatalf("span context = %v, want none", sc)
	}
}

func TestWithTraceparentAdoptsTheCallersTrace(t *testing.T) {
	setTraceContextPropagator(t)

	const (
		traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
		spanID  = "00f067aa0ba902b7"
	)

	t.Setenv("TRACEPARENT", "00-"+traceID+"-"+spanID+"-01")

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

// The identity attributes moved off the four bespoke memory gauges and onto the
// resource, which is what carries them now that otelruntime emits the metrics -
// and otelruntime's instruments take resource attributes only.
func TestNewOTelResourceCarriesProcessIdentity(t *testing.T) {
	tests := []struct {
		name       string
		withDocker string
		want       attribute.KeyValue
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
			t.Fatalf("%s = %v, want %v", want.Key, attr.Value.Emit(), want.Value.Emit())
		}

		return
	}

	t.Fatalf("%s missing from resource, want %v", want.Key, want.Value.Emit())
}

// setTraceContextPropagator installs the same propagator Setup does, since
// WithTraceparent reads it from the global and the default is a no-op.
func setTraceContextPropagator(t *testing.T) {
	t.Helper()

	previous := otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}

// unsetEnv removes key for the duration of the test. t.Setenv registers the restore,
// which plain os.Unsetenv would not - and optIn writes to the environment, so leaking
// it would make the order tests run in observable.
func unsetEnv(t *testing.T, key string) {
	t.Helper()

	t.Setenv(key, "")

	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}
