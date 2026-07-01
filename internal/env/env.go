// Package env provides helpers for reading earth's environment variables,
// including backwards-compatible support for the deprecated EARTHLY_ prefix.
package env

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// DeprecatedPrefix is the legacy environment variable prefix that is being
// replaced by Prefix.
const DeprecatedPrefix = "EARTHLY_"

// Prefix is the current environment variable prefix.
const Prefix = "EARTH_"

// Lookup returns the value of the environment variable identified by suffix,
// preferring the current Prefix and falling back to the deprecated
// DeprecatedPrefix.
//
// NOTE: the DeprecatedPrefix fallback is a temporary shim to support the
// EARTHLY_ -> EARTH_ migration; drop it once EARTHLY_ support is officially
// removed.
func Lookup(suffix string) (string, bool) {
	if v, ok := os.LookupEnv(Prefix + suffix); ok {
		return v, true
	}

	return os.LookupEnv(DeprecatedPrefix + suffix)
}

// DeprecatedWarnings returns a deprecation warning message for each
// DeprecatedPrefix-prefixed variable present in the current environment,
// sorted for deterministic output.
//
// NOTE: This is a temporary shim to support the EARTHLY_ -> EARTH_ migration.
// Remove it (and its tests) once EARTHLY_ support is officially dropped.
func DeprecatedWarnings() []string {
	return warningsFor(os.Environ())
}

// warningsFor is the testable core of DeprecatedWarnings; environ holds entries
// in os.Environ() "KEY=VALUE" form.
func warningsFor(environ []string) []string {
	var warnings []string

	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, DeprecatedPrefix) {
			continue
		}

		replacement := Prefix + strings.TrimPrefix(name, DeprecatedPrefix)
		warnings = append(warnings, fmt.Sprintf("WARNING: %s is deprecated. Use %s.", name, replacement))
	}

	sort.Strings(warnings)

	return warnings
}
