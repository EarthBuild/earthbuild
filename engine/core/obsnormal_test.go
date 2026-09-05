package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Κ₂ is a function of what a step observed, not of how it was written down.
//
// 𝑅 and 𝐷 are maps, so they have no order and no duplicates and the derivation
// sorts them. **𝑁 is a slice.** `sort.Strings` fixes its order and nothing
// removes repeats, so a source that records `/x` twice derives a different key
// from one that records it once - about a step that made exactly the same
// observation.
//
// A real source repeats constantly. A compiler probing include paths stats the
// same absent header once per `-I` directory; a shell's `command -v` walks
// PATH. Whether the repeat reaches the key then depends on the source's
// buffering, so two runs of one build on one machine can key differently - the
// cache misses and nobody can say why.
//
// Worse at S6, where a fleet shares keys: two engines observing identically and
// deriving different keys never share a hit, and the failure looks like a cold
// cache rather than a bug.
//
// The set is the meaning. Green paper (4.6) writes 𝑁 as a set and `sort(𝑁)` in
// the equation is about determinism, not about multiplicity - so the derivation
// has to normalise rather than assume a caller did.
func TestTheObservedKeyIgnoresHowTheObservationWasWrittenDown(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc", "-c", testSource}}}

	once := core.Observation{Negative: []string{testHeaderFile, testHeaderPath}}

	for _, tc := range []struct {
		name string
		obs  core.Observation
	}{{
		// The order a source happened to emit them in.
		name: "the same lookups in another order",
		obs:  core.Observation{Negative: []string{testHeaderPath, testHeaderFile}},
	}, {
		// `cc -I/a -I/b -I/c` stats the same missing header three times.
		name: "a lookup recorded twice",
		obs: core.Observation{Negative: []string{
			testHeaderFile, testHeaderPath, testHeaderFile,
		}},
	}, {
		name: "both at once",
		obs: core.Observation{Negative: []string{
			testHeaderPath, testHeaderFile, testHeaderPath, testHeaderFile,
		}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if core.DeriveObservedKey(n, nil, tc.obs) != core.DeriveObservedKey(n, nil, once) {
				t.Errorf("two ways of writing down one observation derived two keys:"+
					"\n  %v\n  %v", once.Negative, tc.obs.Negative)
			}
		})
	}
}

// A lookup that is genuinely absent still changes the key.
//
// The companion, and the reason normalising is a set operation rather than a
// truncation: collapsing duplicates must not collapse *distinct* paths. Without
// this, "normalise" could be satisfied by discarding 𝑁 entirely - which passes
// the test above and destroys I3.
func TestADistinctNegativeLookupStillChangesTheKey(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc"}}}

	a := core.DeriveObservedKey(n, nil, core.Observation{Negative: []string{"/a"}})
	b := core.DeriveObservedKey(n, nil, core.Observation{Negative: []string{"/a", "/b"}})

	if a == b {
		t.Error("a step that also found /b missing keyed the same as one that did not," +
			" so a base where /b exists would satisfy both")
	}
}
