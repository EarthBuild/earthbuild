package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestTheBuiltinSaysWhetherThisBuildIsAPush.
//
// `ARG EARTHLY_PUSH` is how an Earthfile asks whether this is a push, and
// `tests/dotenv.earth` has a target per answer - `test-with-push` asserts
// "true" and `test-no-push` asserts "false", from the same file. The builtin
// was `false` outright, because there was no push mode for it to report.
//
// Both spellings, because both are supplied: `EARTH_PUSH` is the name and
// `EARTHLY_PUSH` the one every existing Earthfile is written against.
func TestTheBuiltinSaysWhetherThisBuildIsAPush(t *testing.T) {
	t.Parallel()

	const src = `
main:
    FROM alpine:3.22
    ARG EARTHLY_PUSH
    ARG EARTH_PUSH
    RUN echo [$EARTHLY_PUSH] [$EARTH_PUSH]
`

	p, err := interp.Build(versioned+src, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "[false] [false]") {
		t.Errorf("an ordinary build reports %q, and it is not a push", got)
	}

	p, err = interp.Build(versioned+src, testMain, interp.WithPush(true))
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "[true] [true]") {
		t.Errorf("a push build reports %q; the Earthfile cannot tell what kind"+
			" of build it is in", got)
	}
}
