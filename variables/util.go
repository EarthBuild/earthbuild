package variables

import (
	"strings"
)

// ParseKeyValue pases a key-value type into its parts
// if a key value needs to contain a = or \, it must be escaped using '\=', and '\\' respectively
// once an unescaped '=' is found, all remaining chars will be used as-is without the need to be escaped.
// the key and value are returned, along with a bool that is true if a value was defined (i.e. an equal was found)
//
// e.g. ParseKeyValue("foo")       -> `foo`,  “,       false
//
//	ParseKeyValue("foo=")      -> `foo`,  ``,       true
//	ParseKeyValue("foo=bar")   -> `foo`,  `bar`,    true
//	ParseKeyValue(`f\=oo=bar`) -> `f=oo`, `bar`,    true
//	ParseKeyValue(`foo=bar=`)  -> `foo",  `bar=`,   true
//	ParseKeyValue(`foo=bar\=`) -> `foo",  `bar\=`,  true
func ParseKeyValue(s string) (string, string, bool) {
	// Fast path: if there are no backslashes, we can locate '=' using strings.Cut
	// which is highly optimized (SIMD/assembly) and avoids manual byte scanning.
	if !strings.Contains(s, `\`) {
		if before, after, found := strings.Cut(s, "="); found {
			return before, after, true
		}

		return s, "", false
	}

	// Slow path: scan byte-by-byte to handle escaped characters.
	var escaped bool

	for i := range len(s) {
		c := s[i]

		if escaped {
			escaped = false

			continue
		}

		if c == '\\' {
			escaped = true

			continue
		}

		if c == '=' {
			return unescapeKey(s[:i]), s[i+1:], true
		}
	}

	return unescapeKey(s), "", false
}

func unescapeKey(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}

	var sb strings.Builder

	sb.Grow(len(s))

	var escaped bool

	// Note: We scan byte-by-byte instead of rune-by-rune because '\\' is ASCII
	// (< 0x80). Under UTF-8 encoding, ASCII bytes never overlap with multi-byte
	// UTF-8 sequences, making byte-based scanning safe and fast.
	for i := range len(s) {
		c := s[i]

		switch {
		case escaped:
			sb.WriteByte(c)

			escaped = false
		case c == '\\':
			escaped = true
		default:
			sb.WriteByte(c)
		}
	}

	return sb.String()
}

// AddEnv takes in a slice of env vars in key-value format and a new key-value
// string to it, taking care of possible overrides.
func AddEnv(envVars []string, key, value string) []string {
	// Note that this mutates the original slice.
	found := false

	for i, envVar := range envVars {
		k, _, _ := ParseKeyValue(envVar)
		if k == key {
			envVars[i] = key + "=" + value
			found = true

			break
		}
	}

	if !found {
		envVars = append(envVars, key+"="+value)
	}

	return envVars
}
