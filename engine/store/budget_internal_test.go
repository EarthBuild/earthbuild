package store

import (
	"testing"
	"time"
)

// A store that is nearly full is collected without a budget.
//
// **Budget the housekeeping, not the rescue.** The budget exists so that
// routine tidying never makes a guest look unresponsive. It was applied
// equally to a store with a little less room than it wanted and a store with
// almost none - and those are different situations: the first is a cache that
// will be tidied eventually, the second is a build that is about to fail with
// "no space left on device".
//
// Spending longer is now cheap. Collection was moved off the handshake path,
// so a long collection delays the first request rather than killing the
// connection - and a build that waits is strictly better than a build that
// dies part-way through a capture.
//
// Observed: a sweep of 34 targets refilled a store faster than five seconds of
// collecting per guest could clear it, and targets started failing on space
// again with the collector reporting it had stopped early every time.
func TestANearlyFullStoreIsCollectedWithoutABudget(t *testing.T) {
	t.Parallel()

	// The agent's own default lives in engine/guestd; this package only has to
	// know that some budget was asked for.
	const (
		want   = 20 << 30
		budget = 5 * time.Second
	)

	// Comfortable: below target, but with room to work in. The budget applies.
	if got := budgetFor(budget, want, 15<<30); got != budget {
		t.Errorf("a store 5G below target was given %v, wanted the ordinary budget", got)
	}

	// Desperate: almost nothing left, and the next capture is what fails.
	if got := budgetFor(budget, want, 64<<20); got != 0 {
		t.Errorf("a store with 64M free was given a %v budget, wanted no limit", got)
	}

	// An operator who asked for no limit still gets none.
	if got := budgetFor(0, want, 15<<30); got != 0 {
		t.Errorf("an unbudgeted collection was given %v", got)
	}
}
