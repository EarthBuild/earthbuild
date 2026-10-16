package fleet

import (
	"testing"
)

// TestPrimingCountsTowardsWhatTheFleetMoved.
//
// **The bytes went somewhere the account does not look.** Opening a holder's
// connection early moved the base transfer into the prime, and a prime's reply
// is discarded - so a build that fetched 7.9 MiB reported `transfer 417ms for
// 0 B in 0 fetch(es)` and read as a fleet that moved nothing. That is E-F0's
// failure exactly, reintroduced by making the fleet faster (E-F1).
//
// Not counted as a delegated *step*, because priming is not one: a build with
// four steps and two primes that reported six would be an account that quietly
// does not add up, which is the thing this project has fixed most often (E270).
func TestPrimingCountsTowardsWhatTheFleetMoved(t *testing.T) {
	t.Parallel()

	var a account

	a.primed(Reply{FetchedBytes: 8 << 20, FetchMillis: 300})

	got := a.spend()
	if got.Fetched != 8<<20 {
		t.Errorf("a prime moved 8 MiB and the account says %d bytes", got.Fetched)
	}

	if got.Fetches != 1 {
		t.Errorf("%d fetch(es) recorded, want 1", got.Fetches)
	}

	if got.Delegated != 0 {
		t.Errorf("priming was counted as %d delegated step(s): an account that"+
			" reports more steps than the build has is one nobody can check",
			got.Delegated)
	}

	// The slowest fetch is the slowest fetch whoever made it.
	if got.Slowest <= 0 {
		t.Error("a prime's fetch was not considered for the slowest")
	}
}
