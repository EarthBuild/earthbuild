// Package env provides helpers for reading earth's environment variables,
// including backwards-compatible support for the deprecated EARTHLY_ prefix.
package env

import (
	"fmt"
	"os"
	"sort"
	"strconv"
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
	_, value, ok := lookupNamed(suffix)

	return value, ok
}

// lookupNamed is Lookup, additionally reporting which of the two names the value came
// from, so diagnostics can name the variable the user actually set.
func lookupNamed(suffix string) (name, value string, ok bool) {
	name = Prefix + suffix
	if value, ok = os.LookupEnv(name); ok {
		return name, value, true
	}

	name = DeprecatedPrefix + suffix
	value, ok = os.LookupEnv(name)

	return name, value, ok
}

// Bool reports whether the environment variable identified by suffix holds a truthy
// value, honouring the same prefix fallback as Lookup. Unset and empty are false
// without an error; anything strconv.ParseBool rejects is false *with* one, so callers
// can warn instead of silently taking the wrong branch on a typo such as
// EARTH_WITH_DOCKER=yes.
func Bool(suffix string) (bool, error) {
	name, raw, _ := lookupNamed(suffix)
	if raw == "" {
		return false, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s is set to %q, which is not a boolean: use true or false", name, raw)
	}

	return value, nil
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

		suffix, found := strings.CutPrefix(name, DeprecatedPrefix)
		if !found {
			continue
		}

		warnings = append(warnings, fmt.Sprintf("WARNING: %s is deprecated. Use %s.", name, Prefix+suffix))
	}

	sort.Strings(warnings)

	return warnings
}
