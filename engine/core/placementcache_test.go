package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// copyingExec is a COPY that reports where it put what it copied.
type copyingExec struct {
	places []core.Placement
	runs   int
}

func (e *copyingExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.runs++

	return core.Result{Layer: n.ID(), Captured: true, Placements: e.places}, nil
}

// A cached COPY still says where it put things.
//
// The placements are a fact about the step, not about this run of it: the same
// COPY over the same base put the same bytes in the same place, which is what
// its chain key hitting means. Keeping them only on the run that executed made
// the correspondence available exactly once and then never again.
func TestACachedCopyStillReportsItsPlacements(t *testing.T) {
	t.Parallel()

	places := []core.Placement{{Layer: "ctx", From: "src", To: "/w/src"}}

	graph := func() *ir.Graph {
		img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}

		return &ir.Graph{Root: &ir.Node{
			Op:     ir.Op{Kind: ir.OpFile, Args: []string{"src", "/w/"}},
			Inputs: []*ir.Node{img}, Platform: amd64,
		}}
	}

	cache := newMemCache()

	first := &copyingExec{places: places}
	s1 := newSched(cache, allBlobs{}, first)
	s1.Record = &core.Record{}

	if _, err := s1.Run(context.Background(), graph()); err != nil {
		t.Fatal(err)
	}

	if first.runs == 0 {
		t.Fatal("the first build ran nothing")
	}

	// Second build, same graph, same store: the copy hits L1.
	second := &copyingExec{places: places}
	s2 := newSched(cache, allBlobs{}, second)
	s2.Record = &core.Record{}

	if _, err := s2.Run(context.Background(), graph()); err != nil {
		t.Fatal(err)
	}

	var copied *core.StepRecord

	for i, step := range s2.Record.Steps {
		if step.Kind == ir.OpFile {
			copied = &s2.Record.Steps[i]
		}
	}

	if copied == nil {
		t.Fatal("no COPY in the second build's record")
	}

	if copied.Outcome != core.OutcomeL1Hit {
		t.Fatalf("the copy was %v, not a hit - this tests nothing", copied.Outcome)
	}

	if len(copied.Placements) != 1 || copied.Placements[0] != places[0] {
		t.Errorf("a cached COPY reported %v placements, want %v"+
			"\n  without them no traced read under /w/src can be named as a"+
			"\n  checkout path, so the build records no job key", copied.Placements, places)
	}
}
