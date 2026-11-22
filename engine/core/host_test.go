package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A host step is never cached.
//
// It runs unsandboxed on the invoking machine, so nothing bounds what it
// observed: green paper A3 does not hold, ε is not a bound, and any key derived
// from it is a claim about a step that could have read anything. The engine
// already refuses to cache an *unconfined* result; a host step is unconfined by
// definition, and the rule must not depend on an executor remembering to say so.
func TestHostStepsAreNeverCached(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op:   ir.Op{Kind: ir.OpHost, Args: []string{"./release.sh"}},
		Meta: ir.Meta{Source: at(2)},
	}

	cache := newMemCache()

	s := &core.Scheduler{
		Workers: []core.Worker{{ID: testLocal, IsInvoker: true}},
		// An executor that *claims* the result is captured, which is exactly the
		// case the rule has to survive.
		Executor: capturingExec{},
		Cache:    cache,
		Blobs:    allBlobs{},
		Writer:   testStep,
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	if cache.len() != 0 {
		t.Errorf("a host step published %d cache entries, want 0", cache.len())
	}

	if got := s.Record.Steps[0].Outcome; got != core.OutcomeUncaptured {
		t.Errorf("outcome is %v, want uncaptured", got)
	}
}

// And it never hits one that somehow exists.
//
// An entry could be there from a build where the rule was wrong, or from a
// shared cache someone else wrote. Reading it would run nothing and claim the
// machine had been changed.
func TestHostStepsNeverHitTheCache(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op:   ir.Op{Kind: ir.OpHost, Args: []string{"./release.sh"}},
		Meta: ir.Meta{Source: at(2)},
	}

	// Pre-load an entry under the key this step would use.
	cache := newMemCache()
	cache.Put(core.DeriveChainKey(n, nil, nil), core.Entry{Layer: ir.NodeID{9}, Writer: "someone"})

	ran := &countingExec{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: testLocal, IsInvoker: true}},
		Executor: ran,
		Cache:    cache,
		Blobs:    allBlobs{},
		Writer:   testStep,
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	if ran.n != 1 {
		t.Error("a host step was satisfied from the cache instead of being run")
	}
}

type capturingExec struct{}

func (capturingExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	return core.Result{Layer: n.ID(), Captured: true}, nil
}

type countingExec struct{ n int }

func (c *countingExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	c.n++

	return core.Result{Layer: n.ID(), Captured: true}, nil
}
