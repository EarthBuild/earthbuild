package buildkitd

import (
	"strings"
	"testing"
)

func TestAddBuildkitTelemetryEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://otel.example.test")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=Bearer token")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "cicd.pipeline.run.id=123,vcs.revision.id=abc")

	env := map[string]string{}
	addBuildkitTelemetryEnv(env, "earthly-buildkitd", "earthly", true)

	if got := env["OTEL_SERVICE_NAME"]; got != "EarthBuild-buildkitd" {
		t.Fatalf("OTEL_SERVICE_NAME = %q, want EarthBuild-buildkitd", got)
	}
	if got := env["OTEL_METRICS_EXPORTER"]; got != "otlp" {
		t.Fatalf("OTEL_METRICS_EXPORTER = %q, want otlp", got)
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
		"earthbuild.process.role":            "buildkitd",
		"earthbuild.process.nesting":         "inner",
		"earthbuild.buildkit.container.name": "earthly-buildkitd",
		"earthbuild.installation.name":       "earthly",
	}
	for key, want := range wantAttrs {
		if got := attrs[key]; got != want {
			t.Fatalf("resource attr %s = %q, want %q", key, got, want)
		}
	}
}

func TestAddBuildkitTelemetryEnvDoesNothingWithoutMetricsExporter(t *testing.T) {
	env := map[string]string{}
	addBuildkitTelemetryEnv(env, "earthly-buildkitd", "earthly", false)

	if len(env) != 0 {
		t.Fatalf("env = %#v, want empty", env)
	}
}

func parseResourceAttrs(value string) map[string]string {
	attrs := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		key, value, ok := strings.Cut(part, "=")
		if ok {
			attrs[key] = value
		}
	}
	return attrs
}
