package fleet

import (
	"testing"
)

// A bigger machine takes more work before it counts as busy.
//
// The driver balanced on raw outstanding work, which treats a sixty-four core
// machine and a four core one as equals - so a fleet of one large and several
// small machines would give the large one the same share as the rest and finish
// when the small ones did (E272).
//
// What matters is not how many steps a machine is running but **how full it
// is**, and the only party that knows the denominator is the machine.
func TestABiggerMachineTakesMoreWorkBeforeItCountsAsBusy(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "small", at: "s@1", capacity: 2},
		{id: "large", at: "l@2", capacity: 8},
	}

	// Both running two steps: the small one is full, the large one is a quarter
	// full.
	busy := map[string]int{"small": 2, "large": 2}

	got := preferFree(order, nil, busy)
	if got[0].id != "large" {
		t.Errorf("chose %q; two steps on a two-slot machine is full and two on"+
			" an eight-slot machine is a quarter full", got[0].id)
	}
}

// Equal machines behave exactly as they did.
//
// The normalisation reduces to the previous model when every capacity matches,
// which is the common case and every existing test. A change to a cost function
// that quietly altered the equal-capacity case would be a change to every
// placement decision this project has measured.
func TestEqualMachinesPlaceExactlyAsBefore(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "a", at: "a@1", capacity: 4},
		{id: "b", at: "b@2", capacity: 4},
	}

	// A holder wins a tie at equal load...
	if got := preferFree(order, []string{"b@2"}, map[string]int{}); got[0].id != "b" {
		t.Errorf("an idle fleet sent a step away from its base, to %q", got[0].id)
	}

	// ...and loses as soon as it is one step busier.
	got := preferFree(order, []string{"a@1"}, map[string]int{"a": 1})
	if got[0].id != "b" {
		t.Errorf("chose %q; a holder one step busier should yield", got[0].id)
	}
}

// A machine that has not said how big it is is treated as one slot.
//
// The cautious direction: an unknown machine is offered a step and then looks
// full, rather than being treated as infinite and given the whole build. It
// stops being a guess the moment it answers anything, since capacity rides on
// every reply.
func TestAMachineOfUnknownSizeIsTreatedAsSmall(t *testing.T) {
	t.Parallel()

	order := []joined{
		{id: "unknown", at: "u@1"},
		{id: "known", at: "k@2", capacity: 4},
	}

	got := preferFree(order, nil, map[string]int{"unknown": 1, "known": 1})
	if got[0].id != "known" {
		t.Errorf("chose %q; one step on a machine of unknown size fills it,"+
			" and one on a four-slot machine does not", got[0].id)
	}
}
