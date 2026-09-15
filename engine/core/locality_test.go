package core

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAStepGoesWhereItsBaseAlreadyIs.
//
// **A chain is where a fleet loses.** Every step stands on the one before it,
// so a schedule that moves the work moves a layer with it, and a chain of n
// steps placed round-robin ships its base n-1 times (E265). Measured on an
// eight-step chain of 40 MB layers across two machines: `4 delegated, 4 here`,
// **167.9 MiB in 4 fetches**, 86% transfer-bound against three seconds of
// compute. The chain alternated and paid a layer for every handoff.
//
// `fleet.prefer` implements this ordering and its own comment calls it "the
// single most consequential ordering in the fleet". It is called from tests and
// from nowhere else: placement sorts by load and has never been told who holds
// anything.
//
// **Priced, not absolute.** A chain must stay where its base is; a fan-out must
// spread, and almost every build starts `FROM` one common image - so affinity
// that ignored load would put every step of an eight-way parallel build on one
// machine while seven watched, which is worse than no affinity at all. A holder
// wins a tie and loses to a machine that is enough less busy.
func TestAStepGoesWhereItsBaseAlreadyIs(t *testing.T) {
	t.Parallel()

	s := &Scheduler{
		Workers: []Worker{
			{ID: "a", Capacity: 4},
			{ID: "b", Capacity: 4},
		},
	}

	// The step before this one is already going to "b", so its layer will be
	// there and nowhere else.
	base := &ir.Node{Op: ir.Op{Kind: ir.OpScratch}}
	n := &ir.Node{Inputs: []*ir.Node{base}}

	placed := map[ir.NodeID]Worker{base.ID(): {ID: "b"}}

	got, err := s.place(n, map[string]int{"a": 0, "b": 0}, placed)
	if err != nil {
		t.Fatalf("placing: %v", err)
	}

	if got.ID != "b" {
		t.Errorf("placed on %q, want b - where its input is being made, so a"+
			" chain ships its layer at every handoff", got.ID)
	}

	// And a holder that is far busier loses: a fan-out on one common base must
	// still spread, or seven machines watch one work.
	got, err = s.place(n, map[string]int{"a": 0, "b": 8}, placed)
	if err != nil {
		t.Fatalf("placing: %v", err)
	}

	if got.ID != "a" {
		t.Errorf("placed on %q, want a - a holder eight steps deep is not worth"+
			" waiting for, and affinity that ignored load would put an eight-way"+
			" fan-out on one machine", got.ID)
	}

	// A step with no inputs has nowhere it belongs, and load decides.
	rootless := &ir.Node{Op: ir.Op{Kind: ir.OpScratch}}

	got, err = s.place(rootless, map[string]int{"a": 2, "b": 0}, placed)
	if err != nil {
		t.Fatalf("placing: %v", err)
	}

	if got.ID != "b" {
		t.Errorf("a step standing on nothing was placed on %q, want the least"+
			" loaded machine", got.ID)
	}
}
