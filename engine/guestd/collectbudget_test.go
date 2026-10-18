package guestd

import (
	"testing"
	"time"
)

// The collection budget can be raised, because otherwise a device-backed store
// has no way to be collected at all.
//
// **`earth prune` cannot reach it.** Prune collects the host's store directory;
// a microVM's store is a fixed-size image the host has never opened. The agent
// collects it at startup, but under a budget - five seconds, so that
// housekeeping never blocks the handshake - and a busy build writes more than
// five seconds of collecting frees. Observed over one session: a store went
// from 21G free to 5G while collecting on every sandbox start.
//
// Without a way to raise it the only remedy left is remaking the image, which
// discards every layer in it. A setting is the smallest thing that turns "this
// store cannot be collected" into "this store is collected when you ask".
func TestTheCollectionBudgetCanBeRaised(t *testing.T) {
	t.Parallel()

	if got := budgetFrom(""); got != defaultCollectBudget {
		t.Errorf("no setting gave %v, wanted the default %v", got, defaultCollectBudget)
	}

	if got := budgetFrom("10m"); got != 10*time.Minute {
		t.Errorf("10m gave %v", got)
	}

	// Nonsense falls back rather than disabling collection or blocking forever:
	// both of those are worse than the default, and a typo should not choose
	// either.
	if got := budgetFrom("banana"); got != defaultCollectBudget {
		t.Errorf("an unparseable budget gave %v, wanted the default %v", got, defaultCollectBudget)
	}

	// Zero means no budget, which is what an operator asking for a full
	// collection wants and what `earth prune` does on a shared store.
	if got := budgetFrom("0"); got != 0 {
		t.Errorf("0 gave %v, wanted no limit", got)
	}
}
