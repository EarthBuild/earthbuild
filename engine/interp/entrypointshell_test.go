package interp_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A `RUN --entrypoint` written in shell form says so in the plan.
//
// Only the interpreter knows which form the author wrote; only the executor
// knows what the image's entrypoint is. So the form travels and the joining
// happens where both are in hand (E941).
func TestAnEntrypointRecordsWhetherAShellReadsIt(t *testing.T) {
	t.Parallel()

	const body = `
probe:
    FROM alpine:3.22
    RUN --entrypoint -- --flag && ls /tmp
`

	p, err := interp.Build("VERSION 0.8\n"+body, "probe")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	var run *ir.Node

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec {
			run = n
		}
	}

	if run == nil {
		t.Fatal("the plan has no exec step, and the recipe has one")
	}

	if !run.Op.Entrypoint {
		t.Fatal("the step does not carry --entrypoint")
	}

	if !run.Op.EntrypointShell {
		t.Error("a shell-form --entrypoint is not marked as one, so `&&` becomes an argument")
	}
}
