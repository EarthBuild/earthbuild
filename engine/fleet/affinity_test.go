package fleet

import (
	"slices"
	"testing"
)

// A step goes to a machine that already holds its base.
//
// **This is the difference between a fleet that helps and one that cannot.**
// Placement was strict round-robin, so consecutive steps went to different
// workers - and a build is full of chains, where each step's base is the layer
// the step before it produced. Round-robin over a chain of `n` steps therefore
// ships a base `n-1` times, and a base is the largest thing this engine moves.
//
// A step on a worker costs `transfer + compute` and at home costs `compute`, so
// a fleet wins only when the transfer is amortised. Shipping the base every step
// is the exact opposite: it is the arrangement in which adding machines makes a
// build slower while every part of it works correctly, which is what a
// distributed build that is never faster than one machine looks like from the
// inside.
func TestAStepPrefersAMachineThatHoldsItsBase(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "fleet-0", at: "a@host:1"},
		{id: "fleet-1", at: "b@host:2"},
		{id: "fleet-2", at: "c@host:3"},
	}

	got := prefer(order, []string{"c@host:3"})

	if len(got) != len(order) {
		t.Fatalf("preferring dropped workers: %d of %d", len(got), len(order))
	}

	if got[0].id != "fleet-2" {
		t.Errorf("asked %q first for a step whose base is on fleet-2"+
			"\n  every step of a chain would ship its base to a machine that"+
			" does not have it", got[0].id)
	}
}

// Everybody else is still there, in their original order.
//
// A preference is not an exclusion. The holder may have died, be busy, or refuse
// the step, and the fleet must fall through to whoever else can run it - paying
// a transfer rather than failing (I11). Reordering that dropped the rest would
// turn one worker's departure into a build that cannot proceed.
func TestPreferringOneWorkerDoesNotExcludeTheOthers(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "fleet-0", at: "a@host:1"},
		{id: "fleet-1", at: "b@host:2"},
		{id: "fleet-2", at: "c@host:3"},
	}

	got := prefer(order, []string{"b@host:2"})

	ids := make([]string, 0, len(got))
	for _, w := range got {
		ids = append(ids, w.id)
	}

	if !slices.Equal(ids, []string{"fleet-1", "fleet-0", "fleet-2"}) {
		t.Errorf("order came out %v"+
			"\n  the preferred worker first, and the rest as they were - a"+
			" preference that excluded anybody would turn a departure into a"+
			" failed build", ids)
	}
}

// With nobody named, nothing changes.
//
// The first step of a build has no base and no holder, and the rotation is what
// spreads those across the fleet. A preference that reordered on an empty hint
// would quietly serialise every build that starts from a common base.
func TestAnEmptyPreferenceLeavesTheRotationAlone(t *testing.T) {
	t.Parallel()

	order := []joined{{id: "fleet-0", at: "a@1"}, {id: "fleet-1", at: "b@2"}}

	for _, hint := range [][]string{nil, {}, {"nobody@here:9"}} {
		got := prefer(order, hint)

		if len(got) != 2 || got[0].id != "fleet-0" {
			t.Errorf("hint %v reordered a rotation it had nothing to say about", hint)
		}
	}
}

// Two holders both come first, in the order they were named.
//
// A step's base and its sources can be on different machines, and the hint lists
// them nearest-first. Keeping that order matters: the driver named them in the
// order the assignment references them, so the first is the one carrying the
// base - the biggest thing that would otherwise move.
func TestSeveralHoldersKeepTheOrderTheyWereNamedIn(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "fleet-0", at: "a@1"},
		{id: "fleet-1", at: "b@2"},
		{id: "fleet-2", at: "c@3"},
	}

	got := prefer(order, []string{"c@3", "a@1"})

	if got[0].id != "fleet-2" || got[1].id != "fleet-0" || got[2].id != "fleet-1" {
		t.Errorf("order came out %v %v %v", got[0].id, got[1].id, got[2].id)
	}
}
