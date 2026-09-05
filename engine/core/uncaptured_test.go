package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// uncapturedExec runs steps but cannot say what they produced - the state of any
// executor whose layer capture is not yet built.
type uncapturedExec struct{}

func (uncapturedExec) Run(
	_ context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	return core.Result{Exit: 0}, nil // no Layer, and Captured is false
}

// A result whose layer was never captured must not reach the cache.
//
// The zero NodeID is a perfectly well-formed digest, so publishing it stores a
// verified-looking claim that this step produces the empty layer - which every
// later build with the same key would then hit. An executor that cannot capture
// is a slower engine; one that publishes a fabricated digest is a wrong one.
func TestUncapturedResultsAreNotCached(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testTrueWord}}}
	g := &ir.Graph{Root: n}

	cache := newMemCache()

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: uncapturedExec{},
		Cache:    cache,
		Blobs:    allBlobs{},
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if got := cache.len(); got != 0 {
		t.Errorf("cache holds %d entries after an uncaptured result, want 0", got)
	}

	// And it must be visible, not merely absent: a build that cached nothing
	// looks identical to a build that cached everything until the next run.
	if len(s.Record.Steps) != 1 {
		t.Fatalf("want 1 recorded step, got %d", len(s.Record.Steps))
	}

	if s.Record.Steps[0].Outcome != core.OutcomeUncaptured {
		t.Errorf("outcome is %v, want OutcomeUncaptured", s.Record.Steps[0].Outcome)
	}
}
