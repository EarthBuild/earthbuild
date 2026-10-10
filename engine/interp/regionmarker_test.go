package interp

import "testing"

// TestAVariableBesideACommandStillExpands.
//
// The command itself comes back untouched - that is the whole point of standing
// it aside - and the variable around it still expands.
func TestAVariableBesideACommandStillExpands(t *testing.T) {
	t.Parallel()

	s := scope{"FOO": "bar"}

	for _, c := range []struct{ in, want string }{
		{"$FOO-$(echo x)", "bar-$(echo x)"},
		{"$(echo x)-$FOO", "$(echo x)-bar"},
		{"${FOO}$(ls)", "bar$(ls)"},
		// Two regions, and the variable between them.
		{"$(a)$FOO$(b)", "$(a)bar$(b)"},
		// No command at all is the ordinary path and was never affected.
		{"$FOO", "bar"},
		// No variable, and the command is returned as written.
		{"$(echo \"x\")", "$(echo \"x\")"},
	} {
		if got := expandByRegion(c.in, s.expandValue, s.expandWord); got != c.want {
			t.Errorf("expandByRegion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTheMarkerIsSomethingTheExpanderCanRead.
//
// **The marker was a NUL, and the expander is a shell lexer that objects to
// one.** `expandByRegion` stands each `$(...)` aside so the argument can be
// resolved as the single value it is, and hands what is left to
// `scope.expandValue` - which is buildkit's lexer, which prints
//
//	<input>:1:8: invalid character NUL
//
// straight to stderr for every marker it sees. Four such lines came out of one
// run of `tests/build-arg.earth`, in a build that was otherwise working: the
// lexer returns the right answer and complains anyway, so nothing was broken
// and every user saw two error-shaped lines per argument.
//
// The marker only has to be a byte no Earthfile text carries. It does not have
// to be one that makes a lexer shout.
func TestTheMarkerIsSomethingTheExpanderCanRead(t *testing.T) {
	t.Parallel()

	var saw []string

	note := func(in string) string {
		saw = append(saw, in)

		return in
	}

	expandByRegion("$FOO-$(echo x)$(ls)", note, func(in string) string { return in })

	if len(saw) == 0 {
		t.Fatal("the expander was not called")
	}

	for _, in := range saw {
		for _, r := range in {
			if r < 0x20 && r != '\t' && r != '\n' {
				t.Errorf("the expander was handed %q, which carries a control"+
					" character a shell lexer will object to", in)

				break
			}
		}
	}
}

// And nothing of the marker survives into the value.
func TestNoMarkerSurvives(t *testing.T) {
	t.Parallel()

	s := scope{"FOO": "bar"}

	for _, in := range []string{"$FOO-$(echo x)", "$(a)$(b)", "plain"} {
		got := expandByRegion(in, s.expandValue, s.expandWord)
		for _, r := range got {
			if r < 0x20 && r != '\t' && r != '\n' {
				t.Errorf("expandByRegion(%q) = %q, which carries a control character", in, got)

				break
			}
		}
	}
}
