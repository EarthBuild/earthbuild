package core

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Both keys carry the cache epoch, so a generation of entries can be retired.
//
// A cache key describes a claim. When the claim changes - as it did when a
// directory started being keyed on its listing rather than only on its mode
// (E794) - every entry written under the previous claim is still reachable, and
// still wrong: its profile records no listing, so the consistency check has
// nothing to check and passes, and the false hit the fix removed survives in
// every store that already existed. A fix that only applies to empty caches is
// half a fix, and the half it misses is every machine that has built before.
//
// Κ₁ needs it as much as Κ₂ and that is the part worth a test: a false L2 hit
// is *recorded* under the chain key, over a base that is entirely correct, so
// the wrong answer outlives the observation that made it. Measured - a poisoned
// store rebuilt with the fix still served the stale result, as an `L1 hit`.
//
// Neither assertion can pin the value, which would only restate the code. What
// they can do is fail if somebody removes an epoch, which is the mistake worth
// catching.
func TestBothKeysCarryTheCacheEpoch(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"sh", "-c", "true"}}}

	obs := Observation{
		Reads:    map[string]ir.NodeID{},
		Listings: map[string]ir.NodeID{},
	}

	if DeriveObservedKey(n, nil, obs) ==
		deriveObservedKeyAtEpoch(n, nil, obs, cacheEpoch+1) {
		t.Error("the epoch does not reach Κ₂: entries recorded under an older" +
			" meaning of an observation stay reachable, and a cache-semantics" +
			" fix would not apply to any store that already exists")
	}

	// Κ₁ is the half that matters most and the half that was missing. This
	// assertion was written once and silently did not land - the edit that was
	// supposed to add it replaced nothing - so the epoch could be deleted from
	// the chain key with the suite still green, which the catalogue's E795
	// mutant then proved by surviving.
	base := []ir.NodeID{{1}}

	if DeriveChainKey(n, base, nil) ==
		deriveChainKeyAtEpoch(n, base, nil, cacheEpoch+1) {
		t.Error("the epoch does not reach Κ₁, so a result a false L2 hit" +
			" recorded under a correct base survives the fix and is served" +
			" from L1 - which is what a poisoned store was measured doing")
	}
}
