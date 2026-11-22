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

// unquoteKeepingEscapes removes a token's delimiters and leaves its escapes.
//
// **The delimiters are this engine's syntax; the escapes are the value's.** A
// `--flag=value` on DO or BUILD is passed on to something that parses it again -
// `RUN_EARTH` writes it into a shell script - so resolving `\"` here means the
// script's own shell never sees an escape and consumes the bare quote as
// syntax. Measured against buildkit, which strips the delimiters and keeps the
// escapes; matching it is the point (E848a).
//
// The delimiters still go, for the reason unquote records: a quoted token passed
// through whole produced `"wildcard-copy.earth" is not in the build context`, a
// file nobody has, 226 times.
func unquoteKeepingEscapes(s string) string {
	if len(s) >= 2 {
		if q := s[0]; (q == '"' || q == '\'') && s[len(s)-1] == q {
			// Only when the pair delimits the whole token. `"a" and "b"` opens
			// and closes twice, and taking one quote off each end would leave a
			// value nobody wrote - while `"a \"b\" c"` is one token whose inner
			// quotes are escaped and therefore content.
			if !hasBareRune(s[1:len(s)-1], q) {
				return s[1 : len(s)-1]
			}
		}
	}

	return s
}

// hasBareRune reports whether c appears in s outside an escape.
//
// A backslash consumes the byte after it, so `\"` is content and `"` is
// syntax - which is the whole of the difference between one delimited token
// and two.
func hasBareRune(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++

			continue
		}

		if s[i] == c {
			return true
		}
	}

	return false
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
