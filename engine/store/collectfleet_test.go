package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A layer only this machine has outlives one the fleet still holds, however old.
//
// **A fleet holds more than one machine can**, so the two losses are not the
// same price: a layer a peer still has costs a *fetch* to lose, and one nobody
// else has costs a *rebuild*. Least-recently-used alone treats them alike and so
// takes the expensive one first whenever it happens to be older - and "old" and
// "expensive" are unrelated.
func TestWhatOnlyThisMachineHasOutlivesWhatTheFleetHolds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	onlyHere, onPeer := layerIn(t, root, "only-here", 4096), layerIn(t, root, "on-a-peer", 4096)

	// The irrecoverable one is the older, so age alone would take it first.
	usedAt(t, root, onlyHere, -72*time.Hour)
	usedAt(t, root, onPeer, -time.Minute)

	got, err := store.CollectWith(root, 4096, func(id ir.NodeID) bool { return id == onPeer })
	if err != nil {
		t.Fatal(err)
	}

	if got.Removed != 1 {
		t.Fatalf("removed %d layers, wanted 1", got.Removed)
	}

	if stillThere(root, onPeer) {
		t.Error("the recoverable layer was kept and the irrecoverable one taken")
	}

	if !stillThere(root, onlyHere) {
		t.Error("a layer no peer holds went while a recoverable one remained")
	}
}

// Told nothing about the fleet, it collects by age exactly as it always did.
func TestWithoutTheFleetItIsStillLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	old, recent := layerIn(t, root, "old", 4096), layerIn(t, root, "recent", 4096)

	usedAt(t, root, old, -72*time.Hour)
	usedAt(t, root, recent, -time.Minute)

	got, err := store.CollectWith(root, 4096, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got.Removed != 1 || stillThere(root, old) || !stillThere(root, recent) {
		t.Errorf("removed %d; old kept=%v recent kept=%v",
			got.Removed, stillThere(root, old), stillThere(root, recent))
	}
}

func layerIn(t *testing.T, root, name string, size int) ir.NodeID {
	t.Helper()

	var raw [32]byte
	copy(raw[:], name)
	id := ir.NodeID(raw)

	at := filepath.Join(root, "layers", id.String())
	if err := os.MkdirAll(at, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(at, "f"), make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}

	idx, err := store.OpenIndex(root)
	if err != nil {
		t.Fatal(err)
	}

	if err := idx.Note(id); err != nil {
		t.Fatal(err)
	}

	return id
}

func usedAt(t *testing.T, root string, id ir.NodeID, ago time.Duration) {
	t.Helper()

	when := time.Now().Add(ago)
	if err := os.Chtimes(filepath.Join(root, "index", id.String()), when, when); err != nil {
		t.Fatal(err)
	}
}

func stillThere(root string, id ir.NodeID) bool {
	_, err := os.Stat(filepath.Join(root, "layers", id.String()))

	return err == nil
}
