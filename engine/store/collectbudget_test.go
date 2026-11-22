package store_test

import (
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/store"
)

// Collection stops when it is told to, and what it did still counts.
//
// **A collector on the critical path can make a machine unusable.** The guest
// agent collects its store before it serves, so everything collection costs is
// spent inside the host's thirty-second handshake budget. On a store of 44,015
// layers with 5G free that budget was gone before the agent answered anything,
// and every sandbox in a build failed with "the guest did not answer the
// handshake" - a guest that had booted, accepted the connection and was working
// hard on housekeeping nobody was waiting for.
//
// Safe to interrupt by construction, and the loop says so: layers are forgotten
// from the index before they are deleted, so a collection stopped part-way
// leaves an index that *lags* - a store holding more than it claims, which is
// the harmless direction.
func TestCollectionStopsWhenAsked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		usedAt(t, root, layerIn(t, root, name, 4096), -time.Hour)
	}

	// Keep nothing, so without a stop every layer goes.
	calls := 0
	stop := func() bool {
		calls++

		return calls > 2
	}

	got, err := store.CollectUntil(root, 0, nil, stop)
	if err != nil {
		t.Fatal(err)
	}

	if got.Removed == 0 {
		t.Fatal("a collection that was stopped removed nothing, so the budget bought no work")
	}

	if got.Removed >= 5 {
		t.Fatalf("the stop was ignored: %d of 5 layers removed", got.Removed)
	}
}

// With nothing to stop it, collection is what it always was.
func TestCollectionWithoutAStopIsUnchanged(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		usedAt(t, root, layerIn(t, root, name, 4096), -time.Hour)
	}

	got, err := store.CollectUntil(root, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got.Removed != 3 {
		t.Fatalf("removed %d layers, wanted all 3", got.Removed)
	}
}
