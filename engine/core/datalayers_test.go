package core

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step may produce a stack rather than a single layer.
//
// **An image is many layers, and flattening them is a choice this engine makes
// at the wrong end.** A registry hands over one directory per layer; the puller
// merges them into one because a result could only name one layer, and that
// merge is why unpacking has to be serial (E641) and why nothing can be
// assembled at once. Letting a result carry the layers it actually has is the
// enabling change: the puller keeps them apart, and the stack the step stands
// on names each of them.
//
// Order is oldest first, the same order overlayfs stacks a lowerdir list and
// the same order the layers were applied in when they were merged.
func TestAResultMayCarryAStackOfLayers(t *testing.T) {
	t.Parallel()

	s := &Scheduler{stacks: map[ir.NodeID][]ir.NodeID{}, done: map[ir.NodeID]Result{}, Record: &Record{}}

	n := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine:3.22"}}}

	oldest, middle, newest := ir.NodeID{1}, ir.NodeID{2}, ir.NodeID{3}

	s.finish(n, nil, Result{Layers: []ir.NodeID{oldest, middle, newest}}, StepRecord{})

	got := s.StackFor(n)
	want := []ir.NodeID{oldest, middle, newest}

	if !slices.Equal(got, want) {
		t.Errorf("the stack is %v, want %v"+
			"\n  a result carrying several layers must put all of them on the"+
			" stack, oldest first", got, want)
	}
}

// A stack of layers sits above whatever the step already stood on.
func TestACarriedStackSitsAboveTheBase(t *testing.T) {
	t.Parallel()

	s := &Scheduler{stacks: map[ir.NodeID][]ir.NodeID{}, done: map[ir.NodeID]Result{}, Record: &Record{}}

	n := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine:3.22"}}}
	base := []ir.NodeID{{9}}

	s.finish(n, base, Result{Layers: []ir.NodeID{{1}, {2}}}, StepRecord{})

	got := s.StackFor(n)
	want := []ir.NodeID{{9}, {1}, {2}}

	if !slices.Equal(got, want) {
		t.Errorf("the stack is %v, want %v", got, want)
	}
}

// The single-layer form still works, because almost every step uses it.
//
// A RUN produces one delta and says nothing about layers; only an image has
// several. Both spellings reach the same stack.
func TestASingleLayerResultStillStacks(t *testing.T) {
	t.Parallel()

	s := &Scheduler{stacks: map[ir.NodeID][]ir.NodeID{}, done: map[ir.NodeID]Result{}, Record: &Record{}}

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"true"}}}

	s.finish(n, []ir.NodeID{{3}}, Result{Layer: ir.NodeID{7}}, StepRecord{})

	got := s.StackFor(n)
	want := []ir.NodeID{{3}, {7}}

	if !slices.Equal(got, want) {
		t.Errorf("the stack is %v, want %v", got, want)
	}
}
