package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestATargetMayBeginWithADoThatBringsItsOwnBase.
//
// `tests/import.earth+test-command-import` is one line:
//
//	DO command-import+FROM_HELLO_WORLD
//
// and the function it calls begins with a `FROM`. A function is inlined into
// its caller, so that `FROM` is the target's base - the reference reaches it by
// processing the function's commands, and only then finds the requirement
// satisfied.
//
// This engine refused before looking, on the grounds that the target had no
// filesystem yet. True at that instant and not at the next.
//
// **The diagnostic is kept for the case it was written for.** A `DO` of a
// function that establishes nothing still has no filesystem, and still says so -
// after the function has had its chance rather than before.
func TestATargetMayBeginWithADoThatBringsItsOwnBase(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
BASE_IT:
    FUNCTION
    FROM alpine:3.22
    RUN inside-the-function

main:
    DO +BASE_IT
`, testMain)
	if err != nil {
		t.Fatalf("a target beginning with a DO that brings a base was refused: %v", err)
	}

	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "inside-the-function") {
		t.Errorf("the function's steps did not reach the plan:\n%s", got)
	}

	// A function that establishes nothing is still refused, and still says why.
	_, err = interp.Build(versioned+`
NO_BASE:
    FUNCTION
    RUN needs-a-filesystem

main:
    DO +NO_BASE
`, testMain)
	if err == nil {
		t.Fatal("a DO of a function with no base was accepted; there is nothing" +
			" for its commands to run in")
	}

	if !strings.Contains(err.Error(), "filesystem") {
		t.Errorf("refused with %q, which does not say what is missing", err)
	}
}
