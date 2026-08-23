package store

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// **The index may lag the store, never lead it.**
//
// Index says so about itself, and names the consequence: a layer the index
// claims and the store lacks is a cache hit against nothing. This is that
// sentence as a test, and it failed when it was written - Has asked the index
// first and returned on its word, so anything that removed a layer without
// telling the index (a collector, a half-finished copy, a user with `rm`) left
// the store claiming a layer it did not have (E573).
func TestAnIndexThatLeadsTheStoreIsNotBelieved(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	b, err := OpenBlobs(root)
	if err != nil {
		t.Fatal(err)
	}

	id := ir.NodeID{7}

	if err := os.MkdirAll(LayerStore(root).Path(id), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := b.Index().Note(id); err != nil {
		t.Fatal(err)
	}

	if !b.Has(id) {
		t.Fatal("a layer the store holds was reported absent")
	}

	// What a collector, or a user with rm, does. The index is not told.
	if err := os.RemoveAll(LayerStore(root).Path(id)); err != nil {
		t.Fatal(err)
	}

	if !b.Index().Has(id) {
		t.Fatal("the index forgot on its own, so this test proves nothing")
	}

	if b.Has(id) {
		t.Error("the store claims a layer it does not have:" +
			"\n  a cache hit against nothing, which is the one outcome Index exists to prevent")
	}

	// And the disagreement is repaired rather than merely reported, or every
	// build after this one pays the same lookup to reach the same answer.
	if b.Index().Has(id) {
		t.Error("the index still claims the layer after being found wrong")
	}
}

// The other direction is lag, which is allowed and self-heals: the store holds
// a layer the index has not heard of, and asking closes the gap.
func TestAnIndexThatLagsTheStoreCatchesUp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	b, err := OpenBlobs(root)
	if err != nil {
		t.Fatal(err)
	}

	id := ir.NodeID{9}

	if err := os.MkdirAll(LayerStore(root).Path(id), 0o750); err != nil {
		t.Fatal(err)
	}

	if !b.Has(id) {
		t.Fatal("a layer the store holds was reported absent because the index had not heard of it")
	}

	if !b.Index().Has(id) {
		t.Error("the gap was reported and not closed")
	}
}
