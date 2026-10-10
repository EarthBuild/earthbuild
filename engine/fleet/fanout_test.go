package fleet

import (
	"testing"
)

// Independent work still spreads, even when it all shares one base.
//
// The failure mode base affinity introduces, and it is worse than the problem it
// solves. Almost every build starts `FROM` one common image, so once a single
// worker holds that base **every** step prefers it - and a fleet of eight
// machines runs a build on one of them while the other seven watch.
//
// A chain must stay put and a fan-out must spread, and the two pull in opposite
// directions on the same knowledge. What tells them apart is not the graph but
// the fleet: a worker already running a step is not the cheapest place to put
// another one, whatever it holds.
func TestAFanOutFromOneBaseStillSpreads(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "fleet-0", at: "a@1"},
		{id: "fleet-1", at: "b@2"},
		{id: "fleet-2", at: "c@3"},
	}

	// Everything is based on what fleet-0 produced.
	const common = "a@1"

	busy := map[string]int{}

	chosen := map[string]int{}

	// Eight independent steps, placed one after another as a scheduler would.
	for range 8 {
		got := preferFree(order, []string{common}, busy)
		if len(got) == 0 {
			t.Fatal("no worker was offered")
		}

		to := got[0].id
		chosen[to]++
		busy[to]++
	}

	if len(chosen) < 2 {
		t.Fatalf("eight independent steps all went to %v"+
			"\n  a fleet of three ran them on one machine because they shared a"+
			" base; affinity that ignores load is worse than no affinity", chosen)
	}

	// And the machine that holds the base should still have got more than its
	// even share - it is genuinely the cheapest place, just not eight times over.
	if chosen["fleet-0"] <= 8/len(order) {
		t.Errorf("the holder got %d of 8; affinity is not doing anything",
			chosen["fleet-0"])
	}
}

// A chain still stays put, because nothing else is running.
//
// The other half of the same knob. When the fleet is idle the holder is
// unambiguously the cheapest place, and a step that moved anyway would ship a
// base for no reason (E265).
func TestAChainOnAnIdleFleetStillStaysPut(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "fleet-0", at: "a@1"},
		{id: "fleet-1", at: "b@2"},
	}

	busy := map[string]int{}

	got := preferFree(order, []string{"b@2"}, busy)
	if got[0].id != "fleet-1" {
		t.Errorf("an idle fleet sent a step away from its base, to %v", got[0].id)
	}
}

// A holder that is buried under work is not the cheapest place.
//
// The step would wait behind everything already queued there, which costs more
// than fetching a base costs - so the preference has to yield rather than be
// absolute. It is still a preference: the holder keeps its place in the order
// and is used if nobody else can take the step.
func TestABuriedHolderYieldsToAnIdleMachine(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "fleet-0", at: "a@1"},
		{id: "fleet-1", at: "b@2"},
	}

	busy := map[string]int{"fleet-0": 5}

	got := preferFree(order, []string{"a@1"}, busy)
	if got[0].id != "fleet-1" {
		t.Errorf("sent a step to a machine with five already on it, to save one"+
			" base transfer; chose %v", got[0].id)
	}

	// And the holder is still in the list, because a preference is not an
	// exclusion (I11).
	if len(got) != 2 {
		t.Errorf("the busy holder was dropped rather than deprioritised")
	}
}
