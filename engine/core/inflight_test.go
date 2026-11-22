package core

import "testing"

// TestTheBuildRunsAsWideAsTheFleetIs.
//
// **Adding a machine has to add concurrency, and it did not.** The limit
// defaulted to the *driver's* `runtime.NumCPU()` and gates every step through
// one semaphore, delegated ones included - so two sixteen-core machines ran
// sixteen steps at a time and both sat half idle. Measured on 64 steps: 95.90s
// on one machine, 92.27s on two, four waves of sixteen either way (E-F1).
//
// The field conflated two questions. How much work *this* machine takes at once
// is a property of this machine; how much the *build* has in flight is a
// property of the fleet.
func TestTheBuildRunsAsWideAsTheFleetIs(t *testing.T) {
	t.Parallel()

	fleet := []Worker{
		{ID: "local", IsInvoker: true, Capacity: 16},
		{ID: "box", Capacity: 16},
	}

	if got := inFlight(fleet, 0); got != 32 {
		t.Errorf("a build across two sixteen-core machines runs %d steps at"+
			" once, want 32 - so half the fleet is idle", got)
	}

	// **An explicit setting still wins, and has to.** A serial build is how a
	// hang with eight steps in flight is told apart from one that would hang
	// anyway, and a limit the fleet could talk its way out of would not be one.
	if got := inFlight(fleet, 1); got != 1 {
		t.Errorf("EARTH_PARALLELISM=1 gave %d, so a build cannot be made serial", got)
	}

	// A worker that has not said what it holds is not counted as zero and not
	// guessed at: the invoker's own capacity is the floor, which is what every
	// build had before a fleet existed.
	unknown := []Worker{{ID: "local", IsInvoker: true, Capacity: 8}, {ID: "quiet"}}
	if got := inFlight(unknown, 0); got != 8 {
		t.Errorf("a fleet with one silent worker runs %d at once, want 8", got)
	}

	if got := inFlight(nil, 0); got <= 0 {
		t.Errorf("a build with no workers at all runs %d steps at once", got)
	}
}
