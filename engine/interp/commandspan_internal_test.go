package interp

import "testing"

// An escaped `\$(` is text, not a command.
//
// `ARG VAR1="literal\$(string)"` says the value contains the characters
// `$(string)`; the grammar's `escaped-char` is what says so, and `unescape`
// resolves it afterwards. Scanning for `$(` without looking at what precedes it
// ran `string` as a command and failed the build with `"string" exited 127` -
// a command nobody wrote, from a line that quotes it (E783).
func TestAnEscapedSubstitutionIsNotACommand(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, in string
		want     bool
	}{
		{"a plain substitution", "x $(ls) y", true},
		{"an escaped one", `literal\$(string)`, false},
		{"escaped then real", `\$(one) and $(two)`, true},
		{"a doubled backslash does not escape", `\\$(ls)`, true},
		{"no substitution at all", "just text", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			_, _, found := commandSpan(c.in)
			if found != c.want {
				t.Errorf("commandSpan(%q) found = %v, want %v", c.in, found, c.want)
			}
		})
	}
}

// The one it does find is the right one.
//
// "escaped then real" has to resolve `$(two)` and leave `\$(one)` alone, so the
// span must start at the second: skipping is not enough if it also loses the
// place.
func TestTheSpanFoundSkipsPastTheEscapedOne(t *testing.T) {
	t.Parallel()

	in := `\$(one) and $(two)`

	start, end, found := commandSpan(in)
	if !found {
		t.Fatal("no substitution found where one is real")
	}

	if got := in[start+2 : end]; got != "two" {
		t.Errorf("the span holds %q, want two", got)
	}
}

// Escaped dollars are counted, not matched.
//
// `\\$(` is an escaped *backslash* followed by a command that was genuinely
// written, and a rule looking only at the byte before the `$` stands aside a
// command the author meant to run - the mirror of the bug escapedDollar exists
// to fix, and the quieter one, since a command that silently does not run
// leaves no `literalrun` behind to notice.
func TestStandingAsideEscapedDollarsCountsTheBackslashes(t *testing.T) {
	t.Parallel()

	mark := string(escapedDollar)

	for _, tc := range []struct{ in, want string }{
		{`$(echo run)`, `$(echo run)`},                 // written: untouched
		{`\$(echo run)`, mark + `(echo run)`},          // escaped: stood aside
		{`\\$(echo run)`, `\\` + `$(echo run)`},        // escaped backslash, then a command
		{`\\\$(echo run)`, `\\` + mark + `(echo run)`}, // escaped backslash, then an escaped $
		{`\$HOME`, mark + `HOME`},                      // a literal dollar is not only about commands
		{`plain`, `plain`},
		{`ends\`, `ends\`},
	} {
		if got := standAsideEscapedDollar(tc.in); got != tc.want {
			t.Errorf("%q became %q, want %q", tc.in, got, tc.want)
		}
	}

	// What is stood aside comes back as an ordinary dollar, or the mark reaches
	// a user's value and the fix is worse than the defect.
	round := restoreEscapedDollar(standAsideEscapedDollar(`\$(echo run)`))
	if round != `$(echo run)` {
		t.Errorf("the round trip gave %q", round)
	}
}
