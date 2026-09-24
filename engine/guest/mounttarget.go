package guest

import (
	"os"
	"slices"
	"strings"
)

// expandTarget resolves a mount target against the step's environment.
//
// **`$HOME` is the step's, and only the step knows it.** The interpreter cannot
// expand `--mount=target=$HOME/.ssh/id_rsa`: HOME is not a build argument, it is
// the floor this engine gives every step (see stepEnv) or whatever the base
// image declared instead - neither of which the plan has in hand. So the target
// arrived with the dollar sign still in it, the mount landed at a directory
// literally named `$HOME`, and the `test -f $HOME/...` on the same line - where
// a shell had expanded it - looked somewhere else (tests/env-home.earth).
//
// Resolved here for the reason `lookIn` resolves argv[0] here: this is where the
// answer is. A name the step does not have expands to nothing, as a shell does
// it; the alternative is a directory with a dollar sign in its name, which is
// what this replaces.
func expandTarget(target string, env []string) string {
	if !strings.ContainsRune(target, '$') {
		return target
	}

	return os.Expand(target, func(name string) string {
		// Backwards, because a later entry wins: the step's environment is
		// layered floor-then-image-then-ARG, and the last word is the most
		// specific about this step.
		for _, kv := range slices.Backward(env) {
			if k, v, ok := strings.Cut(kv, "="); ok && k == name {
				return v
			}
		}

		return ""
	})
}
