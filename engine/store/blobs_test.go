package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The index answers, and the store is not read.
func TestTheIndexAnswersWithoutReadingTheStore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.NodeID{1}

	err := Publish(root, id, staged(t, root, ".b1", "b1"))
	if err != nil {
		t.Fatal(err)
	}

	b, err := OpenBlobs(root)
	if err != nil {
		t.Fatal(err)
	}

	// The store, gone. The index remains, which is the point: this is what a
	// host sees once the store is a disk it cannot read.
	err = os.RemoveAll(filepath.Join(root, "layers"))
	if err != nil {
		t.Fatal(err)
	}

	if !b.Has(id) {
		t.Fatal("the index was asked about a layer it records and said no," +
			"\n  so the answer came from the store rather than from the index")
	}
}

// A layer the store holds and the index does not is answered, closed and reported.
//
// It can only mean a layer was filed by a path that did not go through Publish.
// While the store is still readable that is observable rather than theoretical,
// and this is the observation.
func TestALayerTheIndexMissedIsReportedAndClosed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.NodeID{2}

	err := Publish(root, id, staged(t, root, ".b2", "b2"))
	if err != nil {
		t.Fatal(err)
	}

	// A path that filed a layer without recording it: the failure this exists
	// to notice.
	err = os.Remove(filepath.Join(root, "index", id.String()))
	if err != nil {
		t.Fatal(err)
	}

	var told []ir.NodeID

	b, err := OpenBlobs(root)
	if err != nil {
		t.Fatal(err)
	}

	b.Gap = func(missed ir.NodeID) { told = append(told, missed) }

	if !b.Has(id) {
		t.Fatal("a layer the store holds was reported absent")
	}

	if len(told) != 1 || told[0] != id {
		t.Fatalf("the gap was not reported: %v", told)
	}

	// Closed, so the next step does not pay the fallback again.
	if !b.index.Has(id) {
		t.Error("the gap was reported and not closed")
	}

	// And reported once, not once per step that wants the layer.
	b.Has(id)
	b.Has(id)

	if len(told) != 1 {
		t.Errorf("one lagging layer was reported %d times", len(told))
	}
}

// A layer nobody has is absent, whichever is asked.
func TestALayerNobodyHasIsAbsent(t *testing.T) {
	t.Parallel()

	b, err := OpenBlobs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if b.Has(ir.NodeID{3}) {
		t.Fatal("an empty store reported holding a layer")
	}
}
