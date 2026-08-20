package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An ordering edge makes a step wait without standing on what it waited for.
//
// This is what WAIT needs and what neither Inputs nor Sources can express.
// Inputs stack a layer, Sources put one in the key; an ordering edge does
// neither, because the thing being waited for is usually a *side effect* - an
// image pushed, a file written on this machine - and there is no layer to take
// from it. Expressing it as an input would stack a filesystem nobody asked for
// and change the result.
func TestAnOrderingEdgeIsWaitedForButNotStacked(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}
	pushed := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"push"}}, Inputs: []*ir.Node{img},
		Meta: ir.Meta{Source: at(2)},
	}
	after := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"depends-on-the-push"}},
		Inputs: []*ir.Node{img},
		After:  []*ir.Node{pushed},
		Meta:   ir.Meta{Source: at(3)},
	}

	e := &recordingExec{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: after})
	if err != nil {
		t.Fatal(err)
	}

	// It stands on the image alone: one layer, not two.
	if got := len(e.bases[at(3)]); got != 1 {
		t.Errorf("the ordered step stands on %d layers, want 1 (the image, not what it waited for)", got)
	}

	for _, id := range e.bases[at(3)] {
		if id == pushed.ID() {
			t.Error("the step it waited for was stacked into its base")
		}
	}

	// And the thing waited for did run: an ordering edge that drops the work is
	// worse than none.
	if _, ran := e.bases[at(2)]; !ran {
		t.Error("the step that was waited for never ran")
	}
}

// An ordering edge does not change what a step produces, so it stays out of the
// identity.
//
// Two builds differing only in ordering do the same work and must share cache
// entries. Putting the edge in the key would make a WAIT block invalidate
// everything after it, which is a cache that punishes the one construct people
// reach for when they need correctness.
func TestAnOrderingEdgeIsNotPartOfIdentity(t *testing.T) {
	t.Parallel()

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}}
	other := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"elsewhere"}}, Inputs: []*ir.Node{base}}

	plain := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"work"}}, Inputs: []*ir.Node{base}}
	ordered := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"work"}}, Inputs: []*ir.Node{base},
		After: []*ir.Node{other},
	}

	if plain.ID() != ordered.ID() {
		t.Error("an ordering edge changed a step's identity, so a WAIT invalidates everything after it")
	}
}
