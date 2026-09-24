package store_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A shared cache's units are collected like everything else.
//
// **They were not, and nothing said so.** `Collect` sweeps `layers/` and the
// `nodes/` a surviving manifest implies, and a portable cache mount files its
// units and its maps in 𝔅 at the store root - a third population the collector
// has never seen. A machine sharing caches grows for ever and `earth prune`
// reports having freed nothing.
//
// Reachability is the same shape as for nodes, one level longer: a pointer in
// `cachemaps/` names a map, and the map names every unit. Anything else at the
// root is a map nothing points at any more, or a unit no map names.
func TestACacheUnitNoMapNamesIsCollected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	live := putBlob(t, root, []byte("a unit some map still names"))
	dead := putBlob(t, root, []byte("a unit nothing names"))

	mapID := putBlob(t, root, []byte("left-pad@1\t"+live.String()+"\n"))
	pointTo(t, root, "npm", "scope", mapID)

	if _, err := store.Collect(root, 1<<40); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if !held(root, live) {
		t.Error("a unit the live map names was collected" +
			"\n  a peer stocking from that map would fetch a unit nobody has")
	}

	if !held(root, mapID) {
		t.Error("the map a pointer names was collected")
	}

	if held(root, dead) {
		t.Error("a unit no map names survived, so a machine that shares caches" +
			" grows for ever and prune reports freeing nothing")
	}
}

// A map no pointer names goes with it.
//
// Every build files a new map for a cache it filled, and the pointer moves. The
// old ones are reachable from nothing and are exactly what accumulates fastest.
func TestASupersededMapIsCollected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	unit := putBlob(t, root, []byte("a unit"))
	old := putBlob(t, root, []byte("stale@1\t"+unit.String()+"\n"))
	now := putBlob(t, root, []byte("fresh@1\t"+unit.String()+"\n"))

	pointTo(t, root, "go-build", "scope", now)

	if _, err := store.Collect(root, 1<<40); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if held(root, old) {
		t.Error("the map this cache used to have survived the pointer moving")
	}

	if !held(root, now) || !held(root, unit) {
		t.Error("the live map or its unit was collected")
	}
}

// A pointer this machine can no longer use is not a reason to keep a map.
//
// The directory is made when a step binds the mount, so its absence means the
// cache is gone - swept by something else, or a scope nothing will ask for
// again. The pointer is bytes, but the map and units behind it are not.
func TestAPointerWithNoCacheDirectoryIsCollected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	unit := putBlob(t, root, []byte("a unit of a cache that is gone"))
	gone := putBlob(t, root, []byte("k@1\t"+unit.String()+"\n"))

	// Deliberately without the mount directory: this is a cache that is gone.
	pointOnly(t, root, "vanished", "scope", gone)

	if _, err := store.Collect(root, 1<<40); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if held(root, gone) || held(root, unit) {
		t.Error("a map for a cache directory that no longer exists was kept")
	}

	if _, err := os.Stat(filepath.Join(root, "cachemaps", "vanished", "scope")); err == nil {
		t.Error("the pointer itself was kept, so it will be re-followed for ever")
	}
}

// The count is reported apart, because a unit is not a layer.
//
// Losing a layer costs a rebuild or a fetch; losing a cache unit costs whatever
// the tool inside does about it, which is usually a download. A store reporting
// them together would read as having thrown away far more than it did - the same
// argument `Nodes` is counted separately for.
func TestCollectedUnitsAreCountedApart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	putBlob(t, root, []byte("one"))
	putBlob(t, root, []byte("two"))

	got, err := store.Collect(root, 1<<40)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if got.Units != 2 {
		t.Errorf("reported %d units swept, want 2", got.Units)
	}

	if got.Removed != 0 {
		t.Errorf("reported %d layers removed, and none were", got.Removed)
	}
}

func putBlob(t *testing.T, root string, body []byte) ir.NodeID {
	t.Helper()

	st, err := blob.New(root)
	if err != nil {
		t.Fatal(err)
	}

	id, _, err := st.Put(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	return id
}

func pointTo(t *testing.T, root, id, scope string, at ir.NodeID) {
	t.Helper()

	// The cache directory the pointer is about, which is what makes it live.
	if err := os.MkdirAll(filepath.Join(root, "mounts", id, scope), 0o750); err != nil {
		t.Fatal(err)
	}

	pointOnly(t, root, id, scope, at)
}

// pointOnly files a pointer with no cache directory behind it.
func pointOnly(t *testing.T, root, id, scope string, at ir.NodeID) {
	t.Helper()

	p := filepath.Join(root, "cachemaps", id, scope)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(at.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func held(root string, id ir.NodeID) bool {
	h := id.String()
	_, err := os.Stat(filepath.Join(root, h[:2], h))

	return err == nil
}
