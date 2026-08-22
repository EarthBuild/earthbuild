package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// disagreements is Index.Disagrees, failing the test rather than returning.
func disagreements(t *testing.T, root string) (missing, claimed []ir.NodeID) {
	t.Helper()

	missing, claimed, err := Index(root).Disagrees()
	if err != nil {
		t.Fatal(err)
	}

	return missing, claimed
}

// Every way a layer enters the store records it.
//
// The risk the index carries is not the lookup, it is completeness: a path that
// files a layer and does not record it costs a rebuild, silently, on a machine
// that has the layer. Publishing is the one seam every such path goes through,
// so this asserts the seam holds for each of them.
func TestEveryWayALayerIsFiledRecordsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Published directly, as a peer transfer and a step's capture both do.
	direct := ir.NodeID{1}

	err := Publish(root, direct, staged(t, root, ".incoming-direct", "a"))
	if err != nil {
		t.Fatal(err)
	}

	// Named, as a build context is.
	named := ir.NodeID{2}

	err = DirStore(root).PutNamed(named, staged(t, root, ".incoming-named", "b"))
	if err != nil {
		t.Fatal(err)
	}

	// Squashed, as a stack too deep to mount is.
	into := ir.NodeID{3}

	err = DirStore(root).Squash(context.Background(), into, []ir.NodeID{direct, named})
	if err != nil {
		t.Fatal(err)
	}

	missing, claimed := disagreements(t, root)

	if len(missing) != 0 {
		t.Errorf("the store holds layers the index does not record: %v"+
			"\n  a machine with the layer would rebuild it, and say nothing", missing)
	}

	if len(claimed) != 0 {
		t.Errorf("the index records layers the store does not hold: %v"+
			"\n  which is a cache hit against a layer that is not there", claimed)
	}
}

// A lost index is rebuilt from the store.
//
// The index is derived, so losing it must cost a walk and not a rebuild of
// every layer. This is also the path a host takes when it meets an existing
// store for the first time.
func TestALostIndexIsRebuiltFromTheStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for i, name := range []string{".s1", ".s2", ".s3"} {
		id := ir.NodeID{byte(i + 1)}

		err := Publish(root, id, staged(t, root, name, name))
		if err != nil {
			t.Fatal(err)
		}
	}

	err := os.RemoveAll(filepath.Join(root, "index"))
	if err != nil {
		t.Fatal(err)
	}

	if Index(root).Has(ir.NodeID{1}) {
		t.Fatal("the index was removed and still answers")
	}

	err = Index(root).Rebuild()
	if err != nil {
		t.Fatal(err)
	}

	missing, claimed := disagreements(t, root)
	if len(missing) != 0 || len(claimed) != 0 {
		t.Fatalf("a rebuilt index disagrees with the store:"+
			"\n  the store holds and the index lacks: %v"+
			"\n  the index claims and the store lacks: %v", missing, claimed)
	}
}

// An unset index holds nothing rather than answering from the working directory.
//
// `Index("")` joins to a *relative* path, so a stray `index/` beside whatever
// directory the process happens to be in would be read as the store's own. A
// wrong "no" costs a rebuild; a wrong "yes" is a cache hit against nothing.
func TestAnUnsetIndexHoldsNothing(t *testing.T) {
	t.Parallel()

	if Index("").Has(ir.NodeID{1}) {
		t.Fatal("an index with no store answered yes")
	}

	err := Index("").Note(ir.NodeID{1})
	if err != nil {
		t.Fatalf("noting into an unset index failed rather than doing nothing: %v", err)
	}
}

// Forgetting a layer nobody recorded is not an error.
//
// Cleanup paths run more than once, and a second forget must not fail in a way
// that masks the first error - the same rule releasing an unknown handle
// follows.
func TestForgettingWhatWasNeverRecordedSucceeds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := Index(root).Forget(ir.NodeID{9})
	if err != nil {
		t.Fatalf("forgetting an unrecorded layer failed: %v", err)
	}
}
