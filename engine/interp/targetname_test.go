package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// `+main` is how a target is named everywhere else, so it is how one may be
// asked for.
//
// The leading `+` is the whole notation: an Earthfile refers to its own targets
// as `+target`, the documentation writes `earth +build`, and every CI script
// spells it that way. Accepting only the bare name meant the first thing anyone
// types is refused - and refused with a message listing `main` as though the
// user had misspelt it.
func TestATargetMayBeNamedWithOrWithoutThePlus(t *testing.T) {
	t.Parallel()

	const src = versioned + `
main:
    FROM alpine:3.22
    RUN build
`

	for _, name := range []string{testMain, "+main"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := interp.Build(src, name)
			if err != nil {
				t.Fatalf("%q was refused: %v", name, err)
			}
		})
	}
}

// A name that is genuinely absent still says so, and still lists what is there.
func TestAMissingTargetStillNamesWhatExists(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
main:
    FROM alpine:3.22
`, "+nope")
	if err == nil {
		t.Fatal("a target that does not exist was built")
	}

	for _, want := range []string{"nope", testMain} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}
