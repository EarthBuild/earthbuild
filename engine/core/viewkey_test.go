package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Two steps binding different sources do not share a chain key.
//
// The reasonable objection to putting a bound view's object in the mount: the
// scheduler already passes the sources' *result layers* as refs, so the bytes
// are in Κ₁ already. They are - but refs say what is available, not what is
// shown. A step with two sources that binds the first and a step that binds the
// second have the same base, the same refs in the same order, and the same
// command; without the object in the mount they key identically, and one is
// served the other's result.
//
// At this level and not the interpreter's, because the interpreter's node
// identity hashes Sources and would differ anyway - a test there passes with
// the field deleted, which is how this one came to be written.
func TestTwoStepsBindingDifferentSourcesKeyDifferently(t *testing.T) {
	t.Parallel()

	first, second := ir.NodeID{1}, ir.NodeID{2}

	step := func(shown ir.NodeID) core.Key {
		n := &ir.Node{
			Op: ir.Op{
				Kind: ir.OpExec, Args: []string{"make"},
				Mounts: []ir.Mount{{Target: "/v", From: shown, View: true, ReadOnly: true}},
			},
		}

		// The same base and the same refs, in the same order, for both: what
		// differs is which of them the step is shown.
		return core.DeriveChainKey(n, []ir.NodeID{{9}}, []ir.NodeID{first, second})
	}

	if step(first) == step(second) {
		t.Error("a step binding its first source and one binding its second" +
			" share a chain key; the mount does not say which it shows, so one" +
			" is served the other's result (I3)")
	}
}
