package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestAFunctionSeesTheCallersTargetName.
//
// `tests/function.earth`'s `TEST_BUILTIN` declares `ARG EARTHLY_TARGET_NAME`
// and asserts it is `test-builtin` - the name of the *target that called it*.
// A function is inlined into its caller and inherits the caller's build
// environment, which the language reference says in the same sentence as the
// build context; the target name is part of that environment.
//
// This engine gave the empty string, because a function's state is built fresh
// and nothing carried the caller's target across. The failure lands four lines
// away as `test "" = "test-builtin"`, which says nothing about where the name
// went.
func TestAFunctionSeesTheCallersTargetName(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
SHOW:
    FUNCTION
    ARG EARTHLY_TARGET_NAME
    RUN echo [$EARTHLY_TARGET_NAME]

main:
    FROM alpine:3.22
    DO +SHOW
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "[main]") {
		t.Errorf("a function saw %q for the target name; it is inlined into the"+
			" caller and the caller is `main`", got)
	}
}
