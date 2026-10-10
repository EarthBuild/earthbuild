package cli

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An export names a node, and a node the graph never reaches is not a build that
// failed to produce something - it is a reading of a target that was never
// scheduled. Both leave an empty stack, and only one of them is worth reporting
// (E895).
func TestOnlyTheGraphsOwnNodesCount(t *testing.T) {
	t.Parallel()

	in := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"in the graph"}}}
	out := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"read but never scheduled"}}}

	got := scheduled(&ir.Graph{Root: in})

	if !got[in.ID()] {
		t.Error("the graph's own root is not counted as scheduled")
	}

	if got[out.ID()] {
		t.Error("a node the graph never reaches is counted as scheduled")
	}

	// A plan with no graph at all must not claim anything ran, or every export
	// would report a step that did not run.
	if len(scheduled(nil)) != 0 {
		t.Error("a nil graph reported scheduled nodes")
	}
}
