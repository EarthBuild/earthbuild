package interp

import "strings"

// unquote removes the delimiters from a quoted token and resolves escapes.
//
// Quotes are *syntax*. The grammar (earthfile.abnf) defines `path` as excluding
// quote characters in the unquoted case and permitting QUOTED-STRING otherwise,
// and `escaped-char = "\" %x21-7E`. Passing a quoted token through as a value
// produced `"wildcard-copy.earth" is not in the build context` - a file nobody
// has, reported as the user's mistake, 226 times across this repository.
//
// Only the outermost delimiters are removed. Quotes *inside* a value are part of
// it: `"say \"hello\""` is `say "hello"`.
func unquote(s string) string {
	if len(s) >= 2 {
		if q := s[0]; (q == '"' || q == '\'') && s[len(s)-1] == q {
			return unescape(s[1 : len(s)-1])
		}
	}

	return unescape(s)
}

// unescape resolves `\x` to `x`, per the grammar's escaped-char.
//
// A trailing lone backslash is left alone rather than swallowing the character
// after it, because there is none - silently dropping it would change the value.
func unescape(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}

	var b strings.Builder

	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] >= 0x21 && s[i+1] <= 0x7E {
			i++
			b.WriteByte(s[i])

			continue
		}

		b.WriteByte(s[i])
	}

	return b.String()
}

// readName reads a variable name after a '$', returning it and how many bytes
// it occupied including any braces.
//
// Used only to *detect* an unexpanded variable in a condition, not to expand
// one: expansion is the shell lexer's job. Detection is this engine's, because
// a condition mentioning something the plan does not know cannot be decided and
// must say which name it was.
func readName(in string) (string, int) {
	if in == "" {
		return "", 0
	}

	if in[0] == '{' {
		end := strings.IndexByte(in, '}')
		if end < 0 {
			return "", 0
		}

		return in[1:end], end + 1
	}

	end := 0
	for end < len(in) && (in[end] == '_' ||
		(in[end] >= 'a' && in[end] <= 'z') ||
		(in[end] >= 'A' && in[end] <= 'Z') ||
		(end > 0 && in[end] >= '0' && in[end] <= '9')) {
		end++
	}

	return in[:end], end
}
