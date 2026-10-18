package interp

import (
	"fmt"
	"strings"
)

// reservedLabels is the namespace the engine stamps its own labels in.
//
// Both spellings, because an image built by either engine carries the other's
// prefix and a reader comparing two images should not have to know which built
// which.
var reservedLabels = []string{"dev.earthly.", "dev.earthbuild."}

// refuseReservedLabel refuses a label in the engine's own namespace.
//
// `tests/reserved-label.earth` exists to be refused and this engine built it
// (E457). The reason is legibility rather than security: a label under
// `dev.earthly.` is the *engine's* statement about the image, and an Earthfile
// that writes one leaves a reader unable to tell an engine's claim from an
// author's.
func refuseReservedLabel(key, where string) error {
	for _, prefix := range reservedLabels {
		if strings.HasPrefix(strings.ToLower(key), prefix) {
			return fmt.Errorf(
				"LABEL %s at %s is in the engine's own namespace"+
					"\n  %s* is where the engine records what it did: use a"+
					" prefix of your own", key, where, prefix)
		}
	}

	return nil
}

// refuseBuiltinArgument refuses an author's value for a name the engine answers.
//
// Two shapes, one rule: `ARG EARTHLY_VERSION=x` gives a default that can never
// apply, and `BUILD +t --EARTHLY_VERSION=x` passes a value that *can* - which
// makes the second the dangerous one, because a target would then be built
// against a version string its caller invented (E457).
//
// The name rather than a list: every builtin this engine supplies is answered by
// `builtinArgs`, so asking it is asking the one authority. A list here would be
// a second one, and the two would agree until somebody added a builtin.
//
// **Only the `EARTH_`/`EARTHLY_` family.** The platform builtins - `TARGETARCH`
// and its siblings - are answers the author may override, and the corpus and
// this repository's own tests both say so: `ARG TARGETARCH=amd64` is how a
// cross-building target states what it is for, and the engine's value applies
// only when no default was written. The first version of this refused those too
// and broke a test that had been asserting it for months. *A rule read off two
// examples is a rule about two examples*.
func refuseBuiltinArgument(name, where, how string) error {
	if !strings.HasPrefix(name, "EARTH_") && !strings.HasPrefix(name, "EARTHLY_") {
		return nil
	}

	if _, engines := builtinArgs("", "", "", "", "", false, false)[name]; !engines {
		return nil
	}

	return fmt.Errorf(
		"%s at %s sets %s, which the engine supplies"+
			"\n  the engine's answer is the only one there can be: declare it"+
			" with `ARG %s` to read it", how, where, name, name)
}
