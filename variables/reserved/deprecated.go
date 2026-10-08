package reserved

import "strings"

const (
	// Prefix is the current prefix for Earthly-specific built-in ARGs.
	Prefix = "EARTH_"

	// DeprecatedPrefix is the legacy prefix for Earthly-specific built-in ARGs.
	// Built-in ARGs using this prefix are deprecated in favour of Prefix.
	DeprecatedPrefix = "EARTHLY_"
)

// DeprecatedBuiltin reports whether name is a deprecated EARTHLY_-prefixed
// built-in ARG. When it is, replacement holds the EARTH_-prefixed built-in ARG
// that supersedes it.
//
// Only names that are both a recognised built-in and have a recognised EARTH_
// equivalent are reported, so callers can safely point users at replacement.
func DeprecatedBuiltin(name string) (replacement string, deprecated bool) {
	suffix, ok := strings.CutPrefix(name, DeprecatedPrefix)
	if !ok {
		return "", false
	}

	if !IsBuiltIn(name) {
		return "", false
	}

	replacement = Prefix + suffix
	if !IsBuiltIn(replacement) {
		return "", false
	}

	return replacement, true
}
