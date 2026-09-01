package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// `--pass-args` hands down the caller's arguments and not the engine's.
//
// A builtin is an answer about the target that declares it, so passing one on
// makes it an answer about somebody else. `ARG EARTHLY_TARGET` in the caller put
// `+test` into scope, `--pass-args` copied the whole scope, and the callee's own
// `ARG EARTHLY_TARGET` found a supplied value and kept it - so a target asked its
// own name and was told its caller's.
//
// `tests/pass-args-no-builtins` is named for this and asserts it in both
// directions; the reference removes reserved arguments from the scope it passes,
// in `RemoveReservedArgsFromScope`, and this engine did not (E943).
func TestPassArgsDoesNotPassBuiltins(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

test:
    FROM alpine:3.22
    ARG EARTHLY_TARGET
    ARG mine=kept
    RUN echo "$EARTHLY_TARGET $mine"
    BUILD --pass-args +other

other:
    FROM alpine:3.22
    ARG EARTHLY_TARGET
    ARG mine=default
    RUN echo "$EARTHLY_TARGET $mine"
`, "test")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var lines []string

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			lines = append(lines, strings.Join(n.Op.Args, " "))
		}
	}

	// **Two steps, and that is the assertion.** With the builtin passed on, the
	// callee echoes the caller's target and the two commands become the same
	// text - so the graph deduplicates them into one node, and the plan has one
	// step where the recipe has two.
	if len(lines) != 2 {
		t.Fatalf("the plan has %d steps and the recipe has two, so the callee"+
			" was told its caller's target: %q", len(lines), lines)
	}

	// The target is git-qualified where the checkout has an origin, so the two
	// are compared with each other rather than with a written-out name.
	if lines[0] == lines[1] {
		t.Errorf("both steps echo %q, and each names its own target", lines[0])
	}

	// The ordinary argument crosses and the builtin does not, which is the whole
	// distinction: `mine=kept` proves --pass-args is working at all, so a green
	// test cannot come from it silently passing nothing.
	for _, l := range lines {
		if !strings.Contains(l, "kept") {
			t.Errorf("a step echoes %q, and the passed argument should reach both", l)
		}
	}
}
