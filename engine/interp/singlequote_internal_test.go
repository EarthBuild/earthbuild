package interp

import "testing"

// A `$(...)` inside single quotes is text, exactly as a shell reads it.
//
// `ARG VAR1='literal$(whoami)string'` is a value containing those characters
// and no command at all - single quotes suppress every expansion, which is the
// one thing they are for. This engine ran `whoami`, and against a `FROM scratch`
// filesystem, so a build asserting the literal failed with `"whoami" exited 1`:
// a command nobody wrote, quoted from a line that says not to run it (E938).
//
// Double quotes are the control. They do not suppress substitution, and a rule
// that treated the two alike would break `LET n=$(echo "$files" | wc -l)`.
func TestASubstitutionInSingleQuotesIsNotACommand(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, in string
		want     bool
	}{
		{"a plain substitution", "x $(ls) y", true},
		{"single quotes suppress it", `'literal$(whoami)string'`, false},
		{"double quotes do not", `"literal$(whoami)string"`, true},
		{"quoted, then real", `'$(one)' and $(two)`, true},
		{"a closed quote does not reach past itself", `'a' $(ls)`, true},
		{"a backslash inside single quotes is literal", `'\' $(ls)`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, _, found := commandSpan(c.in)
			if found != c.want {
				t.Errorf("commandSpan(%q) found = %v, want %v", c.in, found, c.want)
			}
		})
	}

	// The one it finds is still the right one: skipping a quoted region must
	// not also lose the place.
	in := `'$(one)' and $(two)`

	start, end, found := commandSpan(in)
	if !found {
		t.Fatal("no substitution found where one is real")
	}

	if got := in[start+2 : end]; got != "two" {
		t.Errorf("the span holds %q, want two", got)
	}
}

// A dollar inside single quotes is stood aside, for the reason an escaped one is.
//
// Skipping it in the command scan is half the job. The value region is unquoted
// next - which is what quoting is for - and the later scan re-reads the
// unquoted text, where a suppressed command and a written one are the same
// characters. The same trap escapedDollar exists to close, entered from the
// other side.
//
// It covers `$NAME` as well as `$(cmd)`, because single quotes suppress both
// and the pass that expands names runs after the unquoting too.
func TestADollarInSingleQuotesIsStoodAside(t *testing.T) {
	t.Parallel()

	mark := string(escapedDollar)

	for _, tc := range []struct{ in, want string }{
		{`$(echo run)`, `$(echo run)`},                // written: untouched
		{`'$(echo run)'`, `'` + mark + `(echo run)'`}, // quoted: stood aside
		{`'$HOME'`, `'` + mark + `HOME'`},             // a name, suppressed the same way
		{`"$HOME"`, `"$HOME"`},                        // double quotes expand
		{`'a' $HOME`, `'a' $HOME`},                    // the quote closed before it
		{`'\$(x)'`, `'\` + mark + `(x)'`},             // no escapes inside: the backslash stays
		{`\$(echo run)`, mark + `(echo run)`},         // still escaped-aware
	} {
		if got := standAsideEscapedDollar(tc.in); got != tc.want {
			t.Errorf("%q became %q, want %q", tc.in, got, tc.want)
		}
	}

	// What is stood aside comes back an ordinary dollar, or the mark reaches a
	// user's value and the fix is worse than the defect.
	if round := restoreEscapedDollar(standAsideEscapedDollar(`'$(echo run)'`)); round != `'$(echo run)'` {
		t.Errorf("the round trip gave %q", round)
	}
}
