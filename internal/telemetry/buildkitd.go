package telemetry

import (
	"slices"
	"strings"

	"github.com/EarthBuild/earthbuild/internal/telemetry/semconv"
	"go.opentelemetry.io/otel/attribute"
	otelsemconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// BuildkitdEnv returns the OpenTelemetry environment variables the buildkitd
// container should be started with, or nil if telemetry is not opted in under
// environ. Any non-empty OTEL_* variables from environ are copied through, with
// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES overridden to describe buildkitd.
//
// OTEL_* variables are intentionally excluded from buildkitd's Settings.Hash so
// per-run CI metadata in OTEL_RESOURCE_ATTRIBUTES (such as cicd.pipeline.run.id)
// does not restart a running buildkitd container on every invocation.
func BuildkitdEnv(
	containerName, installationName string,
	withDocker bool,
	environ []string,
) map[string]string {
	var env map[string]string

	for _, entry := range environ {
		if !strings.HasPrefix(entry, "OTEL_") {
			continue
		}

		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}

		value = strings.TrimSpace(value)
		if value == "" {
			delete(env, key)

			continue
		}

		if env == nil {
			env = make(map[string]string, 8)
		}

		env[key] = value
	}

	if len(env) == 0 || !enabled(env) {
		return nil
	}

	env[serviceNameEnv] = buildkitdServiceName
	env[resourceAttrEnv] = appendOTELResourceAttributes(
		env[resourceAttrEnv],
		otelsemconv.ContainerName(containerName),
		semconv.InstallationName.String(installationName),
		processNestingAttr(withDocker),
		semconv.ProcessRoleBuildkitd,
	)

	return env
}

// otelResourceAttrValueEscaper percent-encodes the characters the OTel spec requires
// in an OTEL_RESOURCE_ATTRIBUTES value: "," and "=" are the list separators, and "%"
// itself, without which url.PathUnescape on the consumer side errors on a literal
// percent and hands back the raw, untrimmed string. Nothing else is encoded - the
// value stays readable and still round-trips exactly. Replacements are not rescanned,
// so "%" -> "%25" cannot be mangled by the later rules.
//
// Keys are deliberately left alone. The spec asks for them to be encoded too, but
// sdk/resource.fromEnv never decodes keys, so an encoded one would arrive as a literal
// "a%2Cb" - and ours are constants with no separators in them.
var otelResourceAttrValueEscaper = strings.NewReplacer("%", "%25", ",", "%2C", "=", "%3D")

// appendOTELResourceAttributes merges attrs into an inherited
// OTEL_RESOURCE_ATTRIBUTES string, dropping malformed entries and encoding the values
// it adds. attrs wins on a key collision: the inherited value describes the process
// that spawned buildkitd, ours describes buildkitd itself. Emitting the key twice is
// not an option - collectors disagree on which duplicate survives. Inherited entries
// are copied verbatim; they are already encoded by whoever wrote them.
//
// Format:
// https://opentelemetry.io/docs/specs/otel/resource/sdk/#specifying-resource-information-via-an-environment-variable
func appendOTELResourceAttributes(base string, attrs ...attribute.KeyValue) string {
	parts := make([]string, 0, strings.Count(base, ",")+1+len(attrs))
	put := func(key, encodedValue string) {
		prefix := key + "="
		parts = slices.DeleteFunc(parts, func(p string) bool {
			return strings.HasPrefix(p, prefix)
		})

		if encodedValue != "" {
			parts = append(parts, prefix+encodedValue)
		}
	}

	for attr := range strings.SplitSeq(base, ",") {
		key, value, ok := strings.Cut(attr, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		put(key, strings.TrimSpace(value))
	}

	for _, kv := range attrs {
		key := strings.TrimSpace(string(kv.Key))
		if key == "" {
			continue
		}

		val := strings.TrimSpace(kv.Value.String())
		put(key, otelResourceAttrValueEscaper.Replace(val))
	}

	return strings.Join(parts, ",")
}
