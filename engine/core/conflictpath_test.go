package core_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// evictedBlobs is a store that has lost the layer every claim names.
//
// The ordinary consequence of garbage collection: entries and layers are
// evicted on different schedules, and an entry whose layer is gone is a miss
// (Lookup, green paper 4.4). The step then runs again - which is the only way a
// key that already has a claim gets a second one, and therefore the only way a
// non-deterministic step is ever caught by the cache.
type evictedBlobs struct{}

func (evictedBlobs) Has(ir.NodeID) bool { return false }

// nondeterministicExec hands back a different layer every time it is asked.
//
// Which is what a step reading the clock, a random seed, or an unpinned
// dependency does. Deterministic *here* - the sequence is fixed - so the test
// is not itself flaky: the non-determinism under test belongs to the imaginary
// step, not to the run.
type nondeterministicExec struct {
	mu sync.Mutex
	n  byte
}

func (e *nondeterministicExec) Run(
	_ context.Context, _ *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.n++

	return core.Result{Layer: ir.NodeID{e.n}, Captured: true}, nil
}

// fixedExec is the well-behaved counterpart: the same operation, the same layer.
type fixedExec struct{}

func (fixedExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	h := ir.NewHasher()
	for _, a := range n.Op.Args {
		h.Str(a)
	}

	return core.Result{Layer: h.Sum(), Captured: true}, nil
}

func oneStep() *ir.Graph {
	return &ir.Graph{Root: &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"/bin/sh", "-c", "date +%s%N > /out"}},
		Inputs: []*ir.Node{{
			Op:   ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}},
			Meta: ir.Meta{Source: at(2)},
		}},
		Meta: ir.Meta{Source: at(3), Description: "RUN date"},
	}}
}

// A key that claims two results is caught, through a real scheduler and the
// real on-disk cache.
//
// The reporting path was built and unit-tested a piece at a time - the cache
// refuses the rewrite, the warning renders, the front end prints it - and none
// of that establishes that a build can reach it. **A decision verified in
// pieces and never executed whole is the failure this branch has now made three
// times**: the FINALLY artefact nobody read, the SAVE IMAGE nobody wrote, the
// flatten dispatch nobody called.
//
// It is worth the trouble because the first attempt at this test proved the
// opposite of what it was written for. It drove the Κ₂ path - two steps over
// different bases, sharing an observed key - and recorded nothing, because that
// Put is gated on a profile store the front end does not set. That is not a
// defect: S5, the observation source, is declared *simulated* in the plan's
// stage table, so Κ₂ is inert on purpose and will stay inert until real capture
// exists. **What it does mean is that the Κ₁ path is the only one a shipping
// build reaches, so it is the one this has to be tested against.**
//
// Κ₁ takes a second claim exactly when a claim survives the layer it names -
// eviction on different schedules, which is ordinary. The lookup misses, the
// step runs again, and a deterministic step produces the same layer while this
// one does not.
func TestAKeyThatClaimsTwoResultsIsCaught(t *testing.T) {
	t.Parallel()

	ac, err := cache.Open(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}

	exec := &nondeterministicExec{}

	for range 2 {
		s := &core.Scheduler{
			Workers:  []core.Worker{{ID: testLocal}},
			Executor: exec,
			Cache:    ac,
			Blobs:    evictedBlobs{},
			Writer:   testStep,
		}

		_, err = s.Run(context.Background(), oneStep())
		if err != nil {
			t.Fatal(err)
		}
	}

	if n := ac.ConflictCount(); n == 0 {
		t.Fatal("a step produced two results under one key and nothing recorded it")
	}

	// A count with no key in it tells a reader something is wrong and gives
	// them nowhere to look.
	got := ac.Conflicts()
	if len(got) == 0 {
		t.Fatal("the conflict was counted but not recorded")
	}

	if got[0].Held == got[0].Given {
		t.Errorf("a conflict was recorded between a layer and itself: %+v", got[0])
	}
}

// A deterministic step re-run against a lost layer records nothing.
//
// The arm that makes the other one worth having. Eviction is routine, so if
// re-running after it counted as a disagreement, every build that outlived its
// own garbage collection would warn about being correct - and the warning would
// be ignored inside a week, which is the same as not having it.
func TestReRunningADeterministicStepIsNotAConflict(t *testing.T) {
	t.Parallel()

	ac, err := cache.Open(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}

	for range 3 {
		s := &core.Scheduler{
			Workers:  []core.Worker{{ID: testLocal}},
			Executor: fixedExec{},
			Cache:    ac,
			Blobs:    evictedBlobs{},
			Writer:   testStep,
		}

		_, err = s.Run(context.Background(), oneStep())
		if err != nil {
			t.Fatal(err)
		}
	}

	if n := ac.ConflictCount(); n != 0 {
		t.Errorf("re-running a deterministic step was reported as %d conflict(s): %+v",
			n, ac.Conflicts())
	}
}
