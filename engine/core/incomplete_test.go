package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// lossyExec is an executor whose observation source saw *some* of what the step
// read and knows it missed the rest - an eBPF ring buffer that overflowed, a
// tracer that started late.
type lossyExec struct{}

func (lossyExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	return core.Result{
		Layer:    n.ID(),
		Captured: true,
		Observed: true,
		Observation: core.Observation{
			Reads:      map[string]ir.NodeID{"/seen": {1}},
			Listings:   map[string]ir.NodeID{},
			Incomplete: true, // the source dropped events and says so
		},
	}, nil
}

// An incomplete observation must never become a Κ₂ entry.
//
// Κ₂ claims "this step reads exactly these paths, so any base agreeing on them
// yields this result". An observation missing entries makes that claim about a
// step that read more than it recorded, and the first base differing in an
// unrecorded path is a false hit - the one failure the whole design exists to
// prevent (I3).
//
// This is what decides between observation sources. A lossy source is usable
// only if its loss is *detectable*; one that drops events silently cannot be
// used for cache keys at all, however fast it is.
func TestIncompleteObservationsAreNotKeyed(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{testTrueWord}}}

	cache := newMemCache()

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: lossyExec{},
		Cache:    cache,
		Blobs:    allBlobs{},
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	// The chain key is still sound: it is derived from inputs, which are known in
	// full regardless of what the step was seen to read. Only Κ₂ is affected.
	//
	// Asked of the key rather than of the count. Counting entries said "no
	// second key was published" only while there were two keys, and a tier
	// added beside them made it say nothing about Κ₂ at all.
	would := core.DeriveObservedKey(n, nil, core.Observation{
		Reads:      map[string]ir.NodeID{"/seen": {1}},
		Listings:   map[string]ir.NodeID{},
		Incomplete: true,
	})

	if _, published := cache.Get(would); published {
		t.Error("an incomplete observation was published under its observed key")
	}

	if got := s.Record.Steps[0].ObservedKey; got != (core.Key{}) {
		t.Error("an incomplete observation produced an observed-input key")
	}
}
