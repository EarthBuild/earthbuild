package interp

import "testing"

// A `~` in a COPY destination is not a home directory, and the build says so.
//
// The shell expands `~` before a command ever sees it; a COPY destination is
// not a shell word, so `COPY in ~/.` makes a directory literally called `~`.
// The legacy engine has warned about this for years
// (`earthfile2llb/interpreter.go`) and this engine did not, which is one of the
// Native suite's failures: `tests/Earthfile`'s copy-tilde-test asserts the
// message appears for five destinations and, pointedly, does not appear for a
// sixth (E843).
//
// **The sixth is the whole specification.** `some/di~r.` contains a tilde and
// must not warn: the rule is about a *path component* that is `~` or begins
// with one, not about the character appearing anywhere. A substring check
// passes the five cases that matter and fails the one that was written to catch
// it.
func TestATildeInACopyDestinationIsReported(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		dest string
		want bool
	}{
		{"~/.", true},
		{"/some/dir/~/.", true},
		{"~some/dir.", true},
		{"~", true},
		{"/some/dir/~", true},

		{"some/di~r.", false},
		{"/some/dir/.", false},
		{"plain", false},
		{"", false},
		{"/", false},
	} {
		t.Run(c.dest, func(t *testing.T) {
			t.Parallel()

			if got := tildeInDestination(c.dest); got != c.want {
				t.Errorf("tildeInDestination(%q) = %v, want %v", c.dest, got, c.want)
			}
		})
	}
}
