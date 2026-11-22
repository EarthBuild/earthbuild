package fleet_test

import (
	"bytes"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// TestALayerAWorkerFetchedCountsAsUsed.
//
// A guard, not a regression: this held when it was written. It was written
// while chasing a worker that deleted the base it had just fetched, on the
// theory that `fleet.Layers.Put` never records a use and the collector - which
// orders by last use - would therefore take the newest thing in the store
// first. It does not: `OpenIndex` fills from what is on disk, so a fetched
// layer reads as used now.
//
// The real cause was a full disk. Kept anyway, because the property is
// load-bearing and nothing else asserts it: a store whose fetches were
// invisible to its index would collect exactly backwards, and the symptom
// would be a worker that works until it is busy.
func TestALayerAWorkerFetchedCountsAsUsed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	theirs := t.TempDir()
	id := aBiggerLayer(t, theirs)

	packed, err := (&fleet.Layers{Root: theirs}).Get(id)
	if err != nil {
		t.Fatalf("packing: %v", err)
	}

	mine := &fleet.Layers{Root: root}

	got, _, err := mine.Put(bytes.NewReader(packed))
	if err != nil {
		t.Fatalf("putting: %v", err)
	}

	if got != id {
		t.Fatalf("a layer arrived as %v, sent as %v", got, id)
	}

	index, err := store.OpenIndex(root)
	if err != nil {
		t.Fatalf("opening the index: %v", err)
	}

	if !index.Has(id) {
		t.Error("a layer this worker fetched is not in its index, so a" +
			" collection cannot tell it from one nobody has ever used")
	}

	if index.Used(id).IsZero() {
		t.Error("a layer this worker just fetched reports no last use, so the" +
			" collector treats it as the oldest thing in the store and takes" +
			" the base of the step it was fetched for")
	}
}
