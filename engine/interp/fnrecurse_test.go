package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// TestAFunctionMayCallItselfWithDifferentArguments.
//
// `tests/command.earth`'s `RECURSIVE` counts down from 5, touching a file per
// level and stopping at 0 - bounded recursion, guarded by an `IF` on its own
// argument, and the corpus asserts all five files exist and `./0` does not.
//
// Written with `!=` rather than the corpus's `-gt`, which this interpreter
// sends to a probe: the guard being tested is the cycle guard, and a condition
// needing a sandbox would test the harness instead.
//
// The cycle guard keyed on the function's *site* alone - directory and name -
// so a call with a different argument read as a loop and the build was refused
// with `cycle between targets: +RECURSIVE -> +RECURSIVE`. The target memo
// already learned this: it keys on the reference *and* its arguments, because
// the same target with different arguments is a different build.
func TestAFunctionMayCallItselfWithDifferentArguments(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(versioned+`
R:
    FUNCTION
    ARG level=2
    IF [ "$level" != "0" ]
        RUN touch $level
        DO +R --level=0
    END

main:
    FROM alpine:3.22
    DO +R
`, testMain)
	if err != nil {
		t.Fatalf("bounded recursion was refused as a cycle: %v", err)
	}

	// It really recursed: the outer call touches 2, the inner one stops.
	if got := describe(p.Graph.Nodes()); !strings.Contains(got, "touch 2") {
		t.Errorf("the recursion did not happen:\n%s", got)
	}
}

// And a function that calls itself with the *same* arguments is still a cycle,
// because that one does not terminate.
func TestAFunctionCallingItselfUnchangedIsStillACycle(t *testing.T) {
	t.Parallel()

	_, err := interp.Build(versioned+`
R:
    FUNCTION
    ARG level=1
    DO +R --level=$level

main:
    FROM alpine:3.22
    DO +R
`, testMain)
	if err == nil {
		t.Fatal("a function calling itself with its own arguments was accepted," +
			" and there is nothing to stop it")
	}

	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("refused with %q, which does not say what is wrong", err)
	}
}
