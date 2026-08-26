package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestRunEntrypointUsesTheDeclaredEntrypoint.
//
// `RUN --entrypoint` runs the image's entrypoint. The executor reads it from the
// *materialised base's* declaration, which is right when the entrypoint comes
// from a fetched image and wrong when this build declared one: `ENTRYPOINT`
// lands in the interpreter's config and never reaches that declaration.
//
// `tests/gen-dockerfile.earth` is the corpus case - the Dockerfile it generates
// declares `ENTRYPOINT ["echo", "hello world"]` and the target that stands on it
// says `RUN --entrypoint`, which failed with "alpine declares no entrypoint to
// run".
//
// Resolved here when it is known here, so the argv is in the step's key - which
// it should be, an entrypoint being an input to what the step runs.
func TestRunEntrypointUsesTheDeclaredEntrypoint(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

FROM alpine:3.22
ENTRYPOINT ["echo", "hello world"]

main:
    RUN --entrypoint
`, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	found := false

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind != ir.OpExec {
			continue
		}

		found = true

		if got := strings.Join(n.Op.Args, " "); got != "echo hello world" {
			t.Errorf("the step runs %q, want `echo hello world`", got)
		}

		if n.Op.Entrypoint {
			t.Error("the step still asks the executor for the base's entrypoint," +
				" which would prepend it a second time")
		}
	}

	if !found {
		t.Fatal("no step was planned")
	}
}

// With nothing declared here it stays the executor's question, because only the
// fetched image knows.
func TestRunEntrypointStillDefersToTheImage(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(`VERSION 0.8

FROM alpine:3.22

main:
    RUN --entrypoint
`, "main")
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Op.Kind == ir.OpExec && !n.Op.Entrypoint {
			t.Error("the step resolved an entrypoint this plan does not know")
		}
	}
}
