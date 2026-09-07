package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// Nothing is collected while there is room.
//
// **A collector that runs when it need not is a cache thrown away for nothing.**
// The store is only worth having because the next build reads it, so the default
// has to be to keep.
func TestAStoreWithRoomIsLeftAlone(t *testing.T) {
	t.Parallel()

	root := storeWith(t, "a", "b", "c")

	got, err := store.Collect(root, store.Want{Free: 100}, freeIs(1000))
	if err != nil {
		t.Fatal(err)
	}

	if got.Dropped != 0 {
		t.Errorf("%d layers were collected from a store with room", got.Dropped)
	}
}

// Oldest first, and only until there is room.
//
// Least-recently-read is the one order the store can defend: the index records
// when each layer was last used, so what goes is what the builds have stopped
// asking for.
func TestTheLeastRecentlyReadGoFirst(t *testing.T) {
	t.Parallel()

	root := storeWith(t, "old", "middle", "new")
	used(t, root, "old", -72*time.Hour)
	used(t, root, "middle", -24*time.Hour)
	used(t, root, "new", -time.Minute)

	// Room appears after two are gone.
	free := int64(0)
	got, err := store.Collect(root, store.Want{Free: 2}, func(string) (int64, error) {
		return free, nil
	}, store.Freed(func() { free++ }))
	if err != nil {
		t.Fatal(err)
	}

	if got.Dropped != 2 {
		t.Errorf("collected %d layers, wanted 2", got.Dropped)
	}

	if !held(root, "new") {
		t.Error("the most recently read layer was collected")
	}

	if held(root, "old") {
		t.Error("the least recently read layer was kept")
	}
}

// **Forgotten before deleted**, which is the index's own rule: a layer the
// index claims and the store lacks is a cache hit against nothing, and that is
// a wrong build reporting success.
func TestALayerIsForgottenBeforeItIsDeleted(t *testing.T) {
	t.Parallel()

	root := storeWith(t, "gone")

	free := int64(0)
	_, err := store.Collect(root, store.Want{Free: 1}, func(string) (int64, error) {
		return free, nil
	}, store.Freed(func() { free++ }))
	if err != nil {
		t.Fatal(err)
	}

	idx, err := store.OpenIndex(root)
	if err != nil {
		t.Fatal(err)
	}

	if idx.Has(idOf("gone")) {
		t.Error("the index still claims a layer the store no longer holds")
	}
}

// A store whose room cannot be measured is left alone: collecting on a guess
// is throwing away a cache for a number nobody has.
func TestAnUnmeasurableStoreIsLeftAlone(t *testing.T) {
	t.Parallel()

	root := storeWith(t, "a")

	got, err := store.Collect(root, store.Want{Free: 100}, func(string) (int64, error) {
		return 0, os.ErrPermission
	})
	if err == nil {
		t.Fatal("an unmeasurable store was collected anyway")
	}

	if got.Dropped != 0 {
		t.Errorf("%d layers went before the failure", got.Dropped)
	}
}

// freeIs is a store with a fixed amount of room, for the cases that never
// remove anything.
func freeIs(n int64) func(string) (int64, error) {
	return func(string) (int64, error) { return n, nil }
}

func storeWith(t *testing.T, names ...string) string {
	t.Helper()

	root := t.TempDir()

	for _, n := range names {
		at := filepath.Join(root, "layers", idOf(n).String())
		if err := os.MkdirAll(at, 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(at, "f"), []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	idx, err := store.OpenIndex(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range names {
		if err := idx.Note(idOf(n)); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func used(t *testing.T, root, name string, ago time.Duration) {
	t.Helper()

	at := filepath.Join(root, "index", idOf(name).String())
	when := time.Now().Add(ago)

	if err := os.Chtimes(at, when, when); err != nil {
		t.Fatal(err)
	}
}

func held(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, "layers", idOf(name).String()))

	return err == nil
}

func idOf(name string) ir.NodeID {
	var raw [32]byte
	copy(raw[:], name)

	return ir.NodeID(raw)
}

// A layer only this machine has outlives one the fleet still holds, however old.
//
// **A fleet holds more than one machine can**, so the two losses are not the
// same price: a layer a peer still has costs a fetch, and one nobody else has
// costs a rebuild. Age alone would treat them alike and throw away the
// expensive one first, because "expensive" and "old" are unrelated.
func TestWhatOnlyThisMachineHasOutlivesWhatTheFleetHolds(t *testing.T) {
	t.Parallel()

	root := storeWith(t, "only-here", "on-a-peer")

	// The irrecoverable one is the older, so age alone would take it first.
	used(t, root, "only-here", -72*time.Hour)
	used(t, root, "on-a-peer", -time.Minute)

	free := int64(0)
	got, err := store.Collect(root, store.Want{
		Free:      1,
		Elsewhere: func(id ir.NodeID) bool { return id == idOf("on-a-peer") },
	}, func(string) (int64, error) { return free, nil }, store.Freed(func() { free++ }))
	if err != nil {
		t.Fatal(err)
	}

	if got.Dropped != 1 {
		t.Fatalf("collected %d layers, wanted 1", got.Dropped)
	}

	if held(root, "on-a-peer") {
		t.Error("the recoverable layer was kept and the irrecoverable one taken")
	}

	if !held(root, "only-here") {
		t.Error("a layer no peer holds was collected while a recoverable one remained")
	}
}
