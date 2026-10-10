package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `LOCALLY` inside a function applies to what follows the call.
//
// A function is inlined into its caller - this engine says so in `do`'s own
// comment, and acts on it for ENV, WORKDIR and USER: "what the function *set*
// stays set ... exactly as if the lines had been written there". `LOCALLY` is
// the same kind of statement and was the one that did not travel, so the caller
// went back to running in a container after it.
//
// The reference runs a function's recipe on the interpreter it was called from,
// so its `i.local = true` simply persists - there is no restoring step to get
// wrong.
//
// `tests/locally-in-function` is the corpus case and the failure is three hops
// from the cause: the function writes `data` at `$(pwd)` on the machine, the
// caller's next line reads `data` in a container, and the message is
// `cat: can't open 'data'` (E958).
func TestLocallyInsideAFunctionAppliesAfterTheCall(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{
		"sub/Earthfile": versioned + `
SAVES_LOCALLY:
  FUNCTION
  LOCALLY
  RUN echo hi > data
`,
	})

	p, err := interp.Build(versioned+`
test:
  FROM alpine:3.22
  DO ./sub+SAVES_LOCALLY
  RUN cat data
`, "test", interp.WithContext(dir))
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var after *ir.Node

	for _, n := range p.Graph.Nodes() {
		if len(n.Op.Args) == 3 && n.Op.Args[2] == "cat data" {
			after = n
		}
	}

	if after == nil {
		t.Fatal("the step after the call was not planned")
	}

	if after.Op.Kind != ir.OpHost {
		t.Errorf("the step after a LOCALLY function is %v, and the function left the build"+
			" running on this machine", after.Op.Kind)
	}
}

// And a function that does *not* say LOCALLY leaves the caller where it was,
// which is the half that makes the rule a rule rather than a leak.
func TestAFunctionWithoutLocallyLeavesTheCallerInItsContainer(t *testing.T) {
	t.Parallel()

	dir := ctxWith(t, map[string]string{
		"sub/Earthfile": versioned + `
ORDINARY:
  FUNCTION
  RUN echo hi > data
`,
	})

	p, err := interp.Build(versioned+`
test:
  FROM alpine:3.22
  DO ./sub+ORDINARY
  RUN cat data
`, "test", interp.WithContext(dir))
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if len(n.Op.Args) == 3 && n.Op.Args[2] == "cat data" && n.Op.Kind != ir.OpExec {
			t.Errorf("the step after an ordinary function is %v, not a sandboxed one", n.Op.Kind)
		}
	}
}
