package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `+base` is buildable from the command line, as it is referenceable from an
// Earthfile.
//
// The base recipe - the commands before the first target - is a target under the
// reserved name `base`. Other Earthfiles reference it as `FROM ../..+base`, and
// five in this repository do. It is not in the target list, so it is recognised
// by name; the recognition happened before the leading `+` was stripped, so
// `FROM +base` worked and `earth +base` reported that no such target exists -
// while `earth ls` listed it.
//
// *A name matched before it was normalised.* The two spellings are the same
// request everywhere else, which is why `find` trims the `+` at all.
func TestPlusBaseIsBuildableByName(t *testing.T) {
	t.Parallel()

	src := versioned + `
FROM alpine:3.22

main:
    RUN build
`

	for _, name := range []string{"base", "+base"} {
		p, err := interp.Build(src, name)
		if err != nil {
			t.Errorf("build %q: %v", name, err)

			continue
		}

		if p.Graph == nil || len(p.Graph.Nodes()) == 0 {
			t.Errorf("%q planned nothing", name)
		}
	}
}

// A base recipe that sets no image says so, in both spellings.
//
// The diagnosis is the useful part: five Earthfiles in this repository inherit
// from a root recipe that is `VERSION` and some `ARG`s, and "no target named
// base" would send the reader looking for a missing target rather than at the
// recipe that is there and empty.
func TestPlusBaseWithNoImageSaysWhichProblemItIs(t *testing.T) {
	t.Parallel()

	src := versioned + `
ARG FOO=1

main:
    FROM alpine:3.22
    RUN build
`

	for _, name := range []string{"base", "+base"} {
		_, err := interp.Build(src, name)
		if err == nil {
			t.Errorf("%q: a base recipe with no image planned anyway", name)

			continue
		}

		if strings.Contains(err.Error(), "no target named") {
			t.Errorf("%q reported a missing target: %v"+
				"\n  the target is there; it is the recipe that names no image", name, err)
		}
	}
}
