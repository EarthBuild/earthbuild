package ir_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What a step is predicted to read does not change what it is.
//
// The hint has to travel with the node, because the executor materialises a
// step's base and only the node reaches it - and it must not touch identity,
// because a prediction is advice and two engines with different histories would
// otherwise compute different keys for one step (I5, I1).
//
// `Meta` is not hashed, which is why the hint lives there. **Asserted rather
// than assumed**: "this field is not in the key" is exactly the kind of thing
// that is true when written and quietly false two refactors later (E301).
func TestAPredictedReadSetDoesNotChangeWhatAStepIs(t *testing.T) {
	t.Parallel()

	plain := &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{"make"}},
		Meta: ir.Meta{Source: "Earthfile:3"},
	}

	hinted := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"make"}},
		Meta: ir.Meta{
			Source:         "Earthfile:3",
			ReadsPredicted: []string{"usr/bin/cc", "usr/lib/libc.so"},
		},
	}

	if plain.ID() != hinted.ID() {
		t.Errorf("a prediction changed a step's identity: %v against %v"+
			"\n  two engines with different histories would compute different"+
			" keys for one step", plain.ID(), hinted.ID())
	}
}

// Nor does anything else in Meta.
//
// The wider property the one above depends on, and worth its own test: if Meta
// ever starts reaching identity, this says so before the prediction does - and
// says it about the field somebody just added rather than about the prediction.
func TestNothingInMetaChangesWhatAStepIs(t *testing.T) {
	t.Parallel()

	bare := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"make"}}}

	full := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"make"}},
		Meta: ir.Meta{
			Description:    "a description",
			Source:         "Earthfile:9",
			Target:         "+build",
			ReadsPredicted: []string{"anything"},
		},
	}

	if bare.ID() != full.ID() {
		t.Errorf("metadata reached a step's identity: %v against %v"+
			"\n  every field of Meta is something a human or a previous build"+
			" said, and none of it is what the step *is*", bare.ID(), full.ID())
	}
}
