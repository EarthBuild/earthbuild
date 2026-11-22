package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/store"
)

// A layer write that did not finish is reclaimed, not kept for ever.
//
// **The store filled with rubble nothing could clear.** A layer is written to
// `.<id>.partial-<n>` and renamed into place when it is whole, so an
// interrupted write leaves that directory behind. `candidates` parses each
// entry's name as a node id and skips what does not parse - "a name it does not
// understand is not its business" - which is right for a stranger's file and
// wrong for this engine's own debris.
//
// Nothing else removes them, so every failed write leaked its bytes
// permanently. One session of killed guests and ENOSPC mid-copy took a 195G
// store to 7M free, and a collector asked for 20G could not find it: the space
// was in partials it was declining to look at.
//
// Safe to remove at collection time because a store is collected by the agent
// at startup, before any step runs, and a device-backed store is claimed
// exclusively - so no partial can belong to a write in progress. That is the
// same assumption `Prune` already documents.
func TestAnUnfinishedLayerWriteIsReclaimed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	keep := layerIn(t, root, "kept", 4096)
	usedAt(t, root, keep, -time.Minute)

	// The debris of an interrupted write, named as the writer names it.
	partial := filepath.Join(root, "layers",
		".2e5c6835880250b0c63219172c3850eb73487cc387b2d7c50e60c384d894861a.partial-4152642701")

	err := os.MkdirAll(filepath.Join(partial, "usr", "bin"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(partial, "usr", "bin", "earth"), make([]byte, 8192), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// A ceiling far above what the real layer costs, so nothing would be
	// collected on age alone: only the debris should go.
	report, err := store.CollectUntil(root, 1<<30, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Error("an unfinished layer write survived a collection, so its bytes are lost for good")
	}

	if !stillThere(root, keep) {
		t.Error("a finished layer was taken while clearing debris")
	}

	if report.Freed() == 0 {
		t.Error("the report does not account for what clearing the debris freed")
	}
}

// Debris is cleared even when the store has all the room it needs.
//
// **It is garbage, not a cache.** `Reclaim` returns early when the store
// already has the free space asked for, which is right for layers - they are
// worth keeping until the space is actually wanted. A half-written layer is
// worth keeping for exactly no time at all: nothing can use it, and every one
// still on disk makes the free figure the engine trusts a little less true.
//
// Left as it was, debris accumulated through all the healthy time and was only
// ever noticed once the store was short - by which point there was a lot of it
// and the collector had a full store to dig out of.
func TestDebrisGoesEvenWhenThereIsRoom(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	keep := layerIn(t, root, "kept", 4096)
	usedAt(t, root, keep, -time.Hour)

	partial := filepath.Join(root, "layers",
		".8cde42725f59276c0e6647a4f249e353daef8f0e50cc7f76ba8a48f709d843fa.partial-256956288")

	err := os.MkdirAll(partial, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	// One byte wanted free, which any real filesystem already has - so this is
	// the early-return path, where nothing used to happen at all.
	report, err := store.Reclaim(root, 1, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Error("debris survived a store that had room, so it is kept until the store is short")
	}

	if report.Debris != 1 {
		t.Errorf("the report says %d unfinished writes were cleared, wanted 1", report.Debris)
	}

	if !stillThere(root, keep) {
		t.Error("a layer was collected from a store that had room")
	}
}

// Debris goes even when collection is switched off.
//
// **Clearing rubble is not collection.** `want == 0` means "do not collect" -
// a legitimate thing to ask, since layers are worth keeping until the space is
// wanted. It has never meant "keep the remains of writes that were killed",
// and a store told not to collect is exactly the store where debris would
// otherwise pile up untouched for ever.
//
// The same gap as the free-space early return, one level down: a guard written
// for layers silently governing something that is not a layer.
func TestDebrisGoesEvenWhenCollectionIsOff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	keep := layerIn(t, root, "kept", 4096)
	usedAt(t, root, keep, -time.Hour)

	partial := filepath.Join(root, "layers",
		".8cde42725f59276c0e6647a4f249e353daef8f0e50cc7f76ba8a48f709d843fa.partial-1")

	err := os.MkdirAll(partial, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	report, err := store.Reclaim(root, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Error("debris survived a store told not to collect, so switching collection off leaks for ever")
	}

	if report.Debris != 1 {
		t.Errorf("the report says %d unfinished writes were cleared, wanted 1", report.Debris)
	}

	if !stillThere(root, keep) {
		t.Error("a layer was collected from a store told not to collect")
	}
}
