package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TRY runs a step that may fail; FINALLY runs on what it left behind.
//
// The corpus writes exactly one shape of this - `RUN test > report && false`
// followed by `SAVE ARTIFACT report` - and it only works if the failed step's
// filesystem is what FINALLY stands on.
func TestTryMarksItsStepTolerantAndFinallyStandsOnIt(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(tryVersioned+`
main:
    FROM alpine:3.22
    TRY
        RUN produce-then-fail
    FINALLY
        SAVE ARTIFACT data AS LOCAL out
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var tried *ir.Node

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "produce-then-fail") {
			tried = n
		}
	}

	if tried == nil {
		t.Fatalf("the TRY step is not in the graph:\n%s", describe(p.Graph.Nodes()))
	}

	if !tried.Op.Tolerate {
		t.Error("the TRY step is not marked tolerant, so a failure would stop the build there")
	}

	if len(p.Artifacts) != 1 {
		t.Fatalf("FINALLY declared %d artifacts, want 1", len(p.Artifacts))
	}

	// The artifact comes from the step that may have failed - that is where the
	// file it names was written.
	if p.Artifacts[0].From == nil || p.Artifacts[0].From.ID() != tried.ID() {
		t.Error("FINALLY's artifact does not come from the TRY step's filesystem")
	}
}

// Only the TRY step is tolerant: a failure elsewhere still stops the build.
func TestToleranceDoesNotLeakPastTheBlock(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(tryVersioned+`
main:
    FROM alpine:3.22
    TRY
        RUN may-fail
    FINALLY
        SAVE ARTIFACT data AS LOCAL out
    END
    RUN after-the-block
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if strings.Contains(n.Meta.Description, "after-the-block") && n.Op.Tolerate {
			t.Error("a step after the block inherited the block's tolerance")
		}
	}
}
