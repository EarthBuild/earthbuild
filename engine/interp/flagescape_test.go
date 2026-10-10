package interp

import "testing"

// A `--flag=value` keeps its escapes; only the delimiters are syntax.
//
// The two engines disagreed, measured with a three-line Earthfile passing
// `--q='a \"b\" c'` to a function that echoes it:
//
//	native    Q:[a "b" c]
//	buildkit  Q:[a \"b\" c]
//
// It matters because `RUN_EARTH` embeds such a value in a generated shell
// script. With the escapes intact the script's own shell resolves them and the
// pattern keeps its quotes; resolved early, the quotes arrive bare and the shell
// consumes them as syntax - which silently changed 17 assertions in
// `tests/Earthfile` into patterns that cannot match (E848a).
//
// **The delimiters still go.** They are syntax in both engines: a quoted token
// passed through whole produced `"wildcard-copy.earth" is not in the build
// context`, a file nobody has, 226 times.
func TestAFlagValueKeepsItsEscapes(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ in, want string }{
		{`'a \"b\" c'`, `a \"b\" c`},
		{`"a \"b\" c"`, `a \"b\" c`},
		{`a \"b\" c`, `a \"b\" c`},
		{`'file-with-\+.txt'`, `file-with-\+.txt`},
		{`"plain"`, `plain`},
		{`'plain'`, `plain`},
		{`plain`, `plain`},
		{`''`, ``},
		{`'x\\y'`, `x\\y`},

		// Not a delimited token: the quotes are content, and removing the
		// outer pair would take one quote off each end of a value that never
		// had a pair.
		{`"a" and "b"`, `"a" and "b"`},
	} {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()

			if got := unquoteKeepingEscapes(c.in); got != c.want {
				t.Errorf("unquoteKeepingEscapes(%s) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
