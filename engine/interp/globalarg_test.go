package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A `--global` ARG reaches inside a function; a local one does not.
//
// That is the whole distinction the flag draws, and `tests/command-explicit-global.earth`
// asserts both halves in one function - `test "$global_var" != ""` beside
// `test "$local_var" == ""`. This engine gave a function a fresh scope with
// nothing in it, which is right for a local argument and wrong for a global one,
// so the first assertion failed at execution (E425).
//
// A function is a unit with its own interface, and one that silently saw its
// caller's variables would do different things depending on where it was called
// from. `--global` is the author saying "this one, everywhere", which is a
// different statement from "everything".
func TestAGlobalArgReachesInsideAFunctionAndALocalOneDoesNot(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
FROM alpine
ARG --global everywhere=yes
ARG here=no

build:
    DO +SHOW

SHOW:
    FUNCTION
    RUN echo "[$everywhere][$here]"
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	var got string

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && strings.Contains(strings.Join(n.Op.Args, " "), "[") {
			got = strings.Join(n.Op.Args, " ")
		}
	}

	if !strings.Contains(got, "[yes]") {
		t.Errorf("the function did not see the global argument: %s", got)
	}

	// The local one is *undeclared* inside the function, so it reaches the step
	// as its own text and the step's shell expands it to nothing - which is what
	// an undeclared name does everywhere here, and is the observable difference
	// from the global beside it.
	if !strings.Contains(got, "[$here]") {
		t.Errorf("a local argument reached the function: %s"+
			"\n  a function is a unit with its own interface", got)
	}
}

// And the caller may override a global for one call.
func TestAGlobalCanBeOverriddenForOneCall(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
FROM alpine
ARG --global everywhere=yes

build:
    DO +SHOW --everywhere=changed

SHOW:
    FUNCTION
    RUN echo "[$everywhere]"
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	var got string

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && strings.Contains(strings.Join(n.Op.Args, " "), "[") {
			got = strings.Join(n.Op.Args, " ")
		}
	}

	if !strings.Contains(got, "[changed]") {
		t.Errorf("the call's own value did not win: %s", got)
	}
}

// A function calling a function still sees the global.
//
// The mutation sweep found this untested: the value reaching the *first*
// function comes from the caller's state, and only the copy kept on that
// function's own state carries it to a second one. Deleting the copy left every
// test green, because none of them nested a call (E425).
func TestAGlobalSurvivesAFunctionCallingAFunction(t *testing.T) {
	t.Parallel()

	plan, err := interp.Build(`
VERSION 0.8
FROM alpine
ARG --global everywhere=yes

build:
    DO +OUTER

OUTER:
    FUNCTION
    DO +INNER

INNER:
    FUNCTION
    RUN echo "[$everywhere]"
`, "build")
	if err != nil {
		t.Fatalf("%v", err)
	}

	var got string

	for _, n := range plan.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && strings.Contains(strings.Join(n.Op.Args, " "), "[") {
			got = strings.Join(n.Op.Args, " ")
		}
	}

	if !strings.Contains(got, "[yes]") {
		t.Errorf("a global did not survive a nested call: %s", got)
	}
}
