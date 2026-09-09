package core

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// slowBase answers a digest slowly, the way a store on a device does, and
// counts how many it was asked for.
type slowBase struct {
	has    map[string]ir.NodeID
	probes atomic.Int64
}

func (b *slowBase) Digest(path string) (ir.NodeID, bool) {
	b.probes.Add(1)

	d, ok := b.has[path]

	return d, ok
}

func (b *slowBase) ListingDigest(string) (ir.NodeID, bool) { return ir.NodeID{}, false }

// The reason a base is stale does not depend on how many threads looked.
//
// **Determinism is the whole constraint on making this parallel.** WhyStale
// names the first changed path in sorted order, and that string reaches a build
// log and a test's expectations. A scan that reported whichever worker happened
// to finish first would give one answer today and another tomorrow.
func TestTheStaleReasonIsTheFirstPathWhateverTheOrderOfWork(t *testing.T) {
	t.Parallel()

	obs := Observation{Reads: map[string]ir.NodeID{}}
	base := &slowBase{has: map[string]ir.NodeID{}}

	// Two hundred paths, of which three differ. The lowest-sorted of the three
	// is the one to name.
	for i := range 200 {
		at := fmt.Sprintf("/p/%03d", i)
		obs.Reads[at] = ir.NodeID{}

		if i == 40 || i == 90 || i == 150 {
			base.has[at] = ir.NodeID{1}
		} else {
			base.has[at] = ir.NodeID{}
		}
	}

	want := WhyStale(obs, base)

	for range 20 {
		if got := WhyStale(obs, base); got != want {
			t.Fatalf("two runs of the same comparison disagree:\n  %q\n  %q", got, want)
		}
	}

	if !strings.Contains(want, "/p/040") {
		t.Errorf("the reason names %q, not the first differing path", want)
	}
}

// A base that has not changed is checked all the way through, and that is the
// case worth making fast: it is the hit.
func TestAFreshBaseIsCheckedInFull(t *testing.T) {
	t.Parallel()

	obs := Observation{Reads: map[string]ir.NodeID{}}
	base := &slowBase{has: map[string]ir.NodeID{}}

	for i := range 500 {
		at := fmt.Sprintf("/p/%03d", i)
		obs.Reads[at] = ir.NodeID{}
		base.has[at] = ir.NodeID{}
	}

	if why := WhyStale(obs, base); why != "" {
		t.Fatalf("an unchanged base reported stale: %s", why)
	}

	if got := base.probes.Load(); got != 500 {
		t.Errorf("a fresh base cost %d probes for 500 reads; every one has to be"+
			" checked and none should be checked twice", got)
	}
}

// A stale base stops early, so the common case does not pay for the worst one.
func TestAStaleBaseStopsEarly(t *testing.T) {
	t.Parallel()

	obs := Observation{Reads: map[string]ir.NodeID{}}
	base := &slowBase{has: map[string]ir.NodeID{}}

	for i := range 2000 {
		at := fmt.Sprintf("/p/%04d", i)
		obs.Reads[at] = ir.NodeID{}
		base.has[at] = ir.NodeID{}
	}

	// The very first path differs.
	base.has["/p/0000"] = ir.NodeID{9}

	if why := WhyStale(obs, base); why == "" {
		t.Fatal("a changed base reported fresh")
	}

	// Some overshoot is the price of scanning in parallel; scanning everything
	// is not.
	if got := base.probes.Load(); got > 500 {
		t.Errorf("a base whose first path changed cost %d probes of 2000;"+
			" the comparison is meant to stop at the first difference", got)
	}
}
