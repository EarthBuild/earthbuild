package main

import (
	"fmt"
	"strings"
)

// argsAfterTarget reads the build arguments written after the target.
//
// **`earth +target --ARG=value` is how the language passes one.** It is the form
// the documentation uses, the form a person types, and the form this
// repository's corpus uses throughout - often nested, as
// `--target="+create-files --with_docker_ignore=\"true\""`. Only
// `-build-arg NAME=value`, before the target, was understood, so every one of
// those got a usage message instead of a build.
//
// Go's flag package stops at the first word that is not a flag, so the target
// and everything after it arrive here untouched.
//
// **Refused rather than guessed.** A bare word is not a second target and not an
// argument; a `--NAME` with no value names nothing to set. Either is a typed
// intention this cannot carry out, and inventing a meaning for it would be worse
// than saying so (I10).
func argsAfterTarget(rest []string) (map[string]string, error) {
	out := map[string]string{}

	for _, a := range rest {
		name, value, ok := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if !ok || name == "" || !strings.HasPrefix(a, "--") {
			return nil, fmt.Errorf(
				"%q is not a build argument: write --NAME=value after the target,"+
					" or -build-arg NAME=value before it", a)
		}

		// A shell that did not strip them leaves them on, which is how the
		// corpus writes it.
		out[name] = strings.Trim(value, `"`)
	}

	return out, nil
}
