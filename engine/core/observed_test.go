package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// fakeBase is a BaseView over two maps: what exists, and what each directory
// listing hashes to.
type fakeBase struct {
	files    map[string]ir.NodeID
	listings map[string]ir.NodeID
}

func (b fakeBase) Digest(p string) (ir.NodeID, bool) { d, ok := b.files[p]; return d, ok }
func (b fakeBase) ListingDigest(d string) (ir.NodeID, bool) {
	x, ok := b.listings[d]

	return x, ok
}

func digest(b byte) ir.NodeID { var id ir.NodeID; id[0] = b; return id }

var step = &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc", "-c", testSource}}, Platform: amd64}

// TestObservedKeyIgnoresUnreadDifferences is the whole point of Κ₂: two steps
// over *different* bases share a key when they touched nothing that differs.
//
// Under a chain key a base-image bump invalidates everything above it. Under an
// observed-input key it invalidates only what read the changed files, which is
// the improvement plan-native-engine.md §2a-bis exists to deliver.
func TestObservedKeyIgnoresUnreadDifferences(t *testing.T) {
	t.Parallel()

	obs := core.Observation{
		Reads:    map[string]ir.NodeID{testReadPath: digest(1)},
		Listings: map[string]ir.NodeID{testIncludeDir: digest(9)},
	}

	// An equal observation built separately, which is the case that matters:
	// two *builds* observing the same files must agree, and passing one map
	// twice would agree even if the key were derived from the map's address.
	same := core.Observation{
		Reads:    map[string]ir.NodeID{testReadPath: digest(1)},
		Listings: map[string]ir.NodeID{testIncludeDir: digest(9)},
	}

	if core.DeriveObservedKey(step, nil, obs) != core.DeriveObservedKey(step, nil, same) {
		t.Fatal("Κ₂ is not a function of its inputs")
	}
}

// TestNegativeLookupsReachTheKey is E5b's central case.
//
// A step doing `if [ -f /etc/foo ]` reads nothing when the file is absent. A key
// over reads alone would let that step hit its cache against a base where
// /etc/foo exists - a false hit, invariant I3 violated, and a wrong artefact
// delivered silently.
func TestNegativeLookupsReachTheKey(t *testing.T) {
	t.Parallel()

	without := core.Observation{Reads: map[string]ir.NodeID{}}
	with := core.Observation{
		Reads:    map[string]ir.NodeID{},
		Negative: []string{testFlagPath},
	}

	if core.DeriveObservedKey(step, nil, without) == core.DeriveObservedKey(step, nil, with) {
		t.Fatal("a negative lookup did not reach the key: I3 violated")
	}
}

// TestListingsReachTheKey: a step that enumerates a directory depends on its
// contents even if it opens nothing.
func TestListingsReachTheKey(t *testing.T) {
	t.Parallel()

	a := core.Observation{Listings: map[string]ir.NodeID{testPluginDir: digest(1)}}
	b := core.Observation{Listings: map[string]ir.NodeID{testPluginDir: digest(2)}}

	if core.DeriveObservedKey(step, nil, a) == core.DeriveObservedKey(step, nil, b) {
		t.Fatal("a changed directory listing did not reach the key")
	}
}

// TestObservedKeyIsOrderIndependent: the key must not depend on the order the
// observations happened to be recorded in, or the same step keys differently
// between runs.
func TestObservedKeyIsOrderIndependent(t *testing.T) {
	t.Parallel()

	a := core.Observation{
		Reads:    map[string]ir.NodeID{"/a": digest(1), "/b": digest(2)},
		Negative: []string{"/x", "/y"},
	}
	b := core.Observation{
		Reads:    map[string]ir.NodeID{"/b": digest(2), "/a": digest(1)},
		Negative: []string{"/y", "/x"},
	}

	if core.DeriveObservedKey(step, nil, a) != core.DeriveObservedKey(step, nil, b) {
		t.Fatal("Κ₂ depends on recording order")
	}
}

// TestObservedAndChainKeysNeverCollide: the domain tags exist so that a key
// space cannot leak into another. Without them, a chain key over one input
// could coincide with an observed key over one read.
func TestObservedAndChainKeysNeverCollide(t *testing.T) {
	t.Parallel()

	obs := core.Observation{Reads: map[string]ir.NodeID{"/a": digest(1)}}

	if core.DeriveObservedKey(step, nil, obs) == core.DeriveChainKey(step, []ir.NodeID{digest(1)}, nil) {
		t.Fatal("Κ₁ and Κ₂ collided")
	}
}

// TestConsistencyRequiresAbsencesToStillHold is the adversarial half of E5b,
// and the check that decides whether a prediction may be used at all.
func TestConsistencyRequiresAbsencesToStillHold(t *testing.T) {
	t.Parallel()

	obs := core.Observation{
		Reads:    map[string]ir.NodeID{testReadPath: digest(1)},
		Negative: []string{testFlagPath},
	}

	for _, tc := range []struct {
		name string
		base fakeBase
		want bool
	}{
		{"unchanged", fakeBase{files: map[string]ir.NodeID{testReadPath: digest(1)}}, true},

		{
			"a read file changed",
			fakeBase{files: map[string]ir.NodeID{testReadPath: digest(2)}},
			false,
		},

		{
			"a read file vanished",
			fakeBase{files: map[string]ir.NodeID{}},
			false,
		},

		// The one that matters: nothing the step *read* changed, but a file it
		// found absent now exists. Reusing the result here is the false hit.
		{"an absent file appeared", fakeBase{files: map[string]ir.NodeID{
			testReadPath: digest(1),
			testFlagPath: digest(7),
		}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := core.Consistent(obs, tc.base); got != tc.want {
				t.Errorf("Consistent = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestConsistencyChecksListings: a directory whose contents changed invalidates
// a step that enumerated it, even though no file it read was touched.
func TestConsistencyChecksListings(t *testing.T) {
	t.Parallel()

	obs := core.Observation{Listings: map[string]ir.NodeID{testPluginDir: digest(1)}}

	same := fakeBase{listings: map[string]ir.NodeID{testPluginDir: digest(1)}}
	if !core.Consistent(obs, same) {
		t.Error("an unchanged listing was reported inconsistent")
	}

	changed := fakeBase{listings: map[string]ir.NodeID{testPluginDir: digest(2)}}
	if core.Consistent(obs, changed) {
		t.Error("a changed listing was reported consistent")
	}

	gone := fakeBase{listings: map[string]ir.NodeID{}}
	if core.Consistent(obs, gone) {
		t.Error("a vanished directory was reported consistent")
	}
}

// TestConsistencyCostIsProportionalToThePrediction: verification touches only
// the paths the observation names, never the whole tree. A check that walked
// the base would cost more than the rebuild it is trying to avoid.
func TestConsistencyCostIsProportionalToThePrediction(t *testing.T) {
	t.Parallel()

	obs := core.Observation{Reads: map[string]ir.NodeID{"/one": digest(1)}}

	counting := &countingBase{fakeBase{files: map[string]ir.NodeID{"/one": digest(1)}}, 0}

	if !core.Consistent(obs, counting) {
		t.Fatal("unexpected inconsistency")
	}

	if counting.lookups != 1 {
		t.Errorf("checking a one-path prediction made %d lookups, want 1", counting.lookups)
	}
}

type countingBase struct {
	fakeBase

	lookups int
}

func (c *countingBase) Digest(p string) (ir.NodeID, bool) {
	c.lookups++

	return c.fakeBase.Digest(p)
}
