package guest

import "testing"

// TestAMountTargetKnowsTheStepsEnvironment.
//
// **`$HOME` is the step's, and only the step knows it.**
//
//	RUN --mount=type=secret,target=$HOME/.ssh/id_rsa,... test -f $HOME/.ssh/id_rsa
//
// is `tests/env-home.earth`, entire. The interpreter cannot expand that target:
// HOME is not a build argument, it is the floor this engine gives every step
// (stepEnv) or whatever the base image declared instead. So the target arrived
// with the dollar sign still in it, the secret was mounted at a directory
// literally called `$HOME`, and the `test -f` on the same line - where a shell
// *had* expanded it - looked somewhere else.
//
// Resolved here for the same reason `lookIn` resolves argv[0] here: this is
// where the answer is.
func TestAMountTargetKnowsTheStepsEnvironment(t *testing.T) {
	t.Parallel()

	env := []string{"HOME=/root", "PATH=/usr/bin", "EMPTY="}

	for _, c := range []struct{ in, want string }{
		{"$HOME/.ssh/id_rsa", "/root/.ssh/id_rsa"},
		{"${HOME}/.cache", "/root/.cache"},
		{"/plain/path", "/plain/path"},
		// A name the step does not have expands to nothing, as a shell does it -
		// the alternative is a directory with a dollar sign in its name.
		{"$NOPE/x", "/x"},
		{"$EMPTY/x", "/x"},
		// Nothing to expand, and a `$` that is not a name is left alone.
		{"", ""},
		{"/a$", "/a$"},
	} {
		if got := expandTarget(c.in, env); got != c.want {
			t.Errorf("expandTarget(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
