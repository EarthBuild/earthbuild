package exec

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAnImageIsRefusedOnlyIfALayerItHoldsLeaked.
//
// **The exit point, not the step.** A layer holding a credential has gone
// nowhere while it sits in this build's store; saving the image is what sends it
// somewhere else. So the finding is recorded where it is found and the refusal
// happens here, once per image rather than once per step - and a build that
// exports nothing cannot leak anything and pays nothing.
//
// Only layers this build produced are ever recorded. A base layer arrived before
// the build did and is read-only to it, so it cannot hold a credential this
// build was handed.
func TestAnImageIsRefusedOnlyIfALayerItHoldsLeaked(t *testing.T) {
	ours := ir.NodeID{1}
	base := ir.NodeID{2}
	other := ir.NodeID{3}

	var e Executor

	e.noteLeaked(ours, []string{"TOKEN in app.env"})

	// A stack with nothing of ours in it is written.
	err := e.RefuseLeakedImage("Earthfile:9", []ir.NodeID{base, other})
	if err != nil {
		t.Errorf("an image with no finding was refused: %v", err)
	}

	// One that carries the tainted layer is not.
	err = e.RefuseLeakedImage("Earthfile:9", []ir.NodeID{base, ours})
	if err == nil {
		t.Fatal("an image carrying a leaked layer was written")
	}

	for _, want := range []string{"TOKEN", "app.env", "Earthfile:9"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n  %v", want, err)
		}
	}

	// And somebody who means it can say so.
	t.Setenv(EnvAllowLeakedSecrets, "1")

	err = e.RefuseLeakedImage("Earthfile:9", []ir.NodeID{base, ours})
	if err != nil {
		t.Errorf("the escape hatch did not open: %v", err)
	}
}

// Nothing recorded is the ordinary case and must cost nothing to ask about.
func TestAnImageWithNoFindingsIsNotRefused(t *testing.T) {
	t.Parallel()

	var e Executor

	err := e.RefuseLeakedImage("Earthfile:1", []ir.NodeID{{9}})
	if err != nil {
		t.Errorf("a build that recorded nothing was refused: %v", err)
	}
}
