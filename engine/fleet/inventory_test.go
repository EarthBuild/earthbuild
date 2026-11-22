package fleet_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A fleet that never assembles does not hang the build.
//
// `WaitFor` returns what joined when the context ends. Fewer machines is a
// **different inventory** and therefore a different schedule, which is honest -
// the build happens with what turned up and nothing pretends otherwise. Blocking
// for ever would make one absent worker into a build that never finishes, which
// is the failure I11 exists to rule out.
func TestAFleetThatNeverAssemblesDoesNotHangTheBuild(t *testing.T) {
	t.Parallel()

	r := &fleet.Rendezvous{}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()

	if got := r.WaitFor(ctx, 3); got != 0 {
		t.Errorf("WaitFor reported %d workers of a fleet nobody joined", got)
	}

	if time.Since(start) > 5*time.Second {
		t.Error("WaitFor outlived its context; one absent worker must not be a" +
			" build that never finishes")
	}
}

// The schedule depends on how many joined, not on which or when.
//
// §4.7.3 requires a byte-identical schedule from the same graph and the same
// worker inventory. Placement is decided in one pass **before** the build starts
// so that it is a pure function rather than a race with whatever connected
// first - and the inventory is therefore named by position, not by endpoint
// identity.
//
// The identity decides *who* runs a step, which the fleet settles at assign
// time; the inventory decides *how many run at once*, and only that reaches the
// schedule. Mixing the two would make a build's schedule change because the same
// machines dialled in a different order.
func TestTheScheduleDependsOnHowManyJoinedAndNotOnWhich(t *testing.T) {
	t.Parallel()

	// Two inventories of the same size, as two different sets of machines would
	// produce.
	first := namesOf(inventoryOfSize(t, 3))
	second := namesOf(inventoryOfSize(t, 3))

	if !slices.Equal(first, second) {
		t.Errorf("two fleets of three produced %v and %v; the schedule would"+
			" differ because different machines connected", first, second)
	}

	if len(namesOf(inventoryOfSize(t, 2))) == len(first) {
		t.Error("a fleet of two and a fleet of three produced the same" +
			" inventory; how many machines there are must reach the schedule")
	}
}

// inventoryOfSize is what a rendezvous of this many workers reports.
func inventoryOfSize(t *testing.T, n int) []core.Worker {
	t.Helper()

	r := &fleet.Rendezvous{}

	for range n {
		r.AddForTest()
	}

	return r.Inventory()
}

func namesOf(ws []core.Worker) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.ID)
	}

	return out
}
