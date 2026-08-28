package core

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Κ₂ carries an epoch, so a change in what an observation *means* retires the
// entries recorded under the old meaning.
//
// A cache key describes a claim. When the claim changes - as it did when a
// directory started being keyed on its listing rather than only on its mode
// (E794) - every entry written under the previous claim is still reachable, and
// still wrong: its profile records no listing, so the consistency check has
// nothing to check and passes, and the false hit the fix removed survives in
// every store that already existed. A fix that only applies to empty caches is
// half a fix, and the half it misses is every machine that has built before.
//
// This asserts the epoch reaches the key. It cannot assert the value, which
// would only pin what the code already says; what it can do is fail if somebody
// removes the epoch, which is the mistake worth catching.
func TestTheObservedKeyCarriesItsEpoch(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"sh", "-c", "true"}}}

	obs := Observation{
		Reads:    map[string]ir.NodeID{},
		Listings: map[string]ir.NodeID{},
	}

	got := DeriveObservedKey(n, nil, obs)

	if got == deriveObservedKeyAtEpoch(n, nil, obs, observedEpoch+1) {
		t.Error("the epoch does not reach Κ₂: entries recorded under an older" +
			" meaning of an observation stay reachable, and a cache-semantics" +
			" fix would not apply to any store that already exists")
	}
}
