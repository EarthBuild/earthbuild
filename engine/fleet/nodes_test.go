package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A worker serves the content-addressed nodes of its store.
//
// **The gap between what the fleet moves and what a cache needs.** The fleet
// moves layers and fragments of layers; a store's `nodes/` - REAPI Directory
// messages, and anything else filed under ℋ over its own bytes - it has never
// touched. A cache shared between machines is made of exactly those, so without
// this the read-through in `remote.Cache` has nobody to read through to.
//
// Nothing new is invented for it: a node is a whole blob, `Held` is the
// interface the blob server already asks, and `earth/blob/1` already carries
// them verified per chunk.
func TestAWorkerServesTheNodesItHolds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	body := []byte("a directory message, or an object, or anything named by ℋ")
	id := putNode(t, root, body)

	n := &Nodes{Root: root}

	if !n.Has(id) {
		t.Fatal("a node this worker holds is not claimed, so nobody will ask for it")
	}

	got, err := n.Get(id)
	if err != nil {
		t.Fatalf("get a held node: %v", err)
	}

	if string(got) != string(body) {
		t.Errorf("served %q, want %q", got, body)
	}
}

// A node this worker does not hold is not claimed.
//
// `Has` is what the blob server checks before sending, so a worker claiming
// something it lacks is a peer every asker dials and nobody is served by.
func TestANodeThisWorkerLacksIsNotClaimed(t *testing.T) {
	t.Parallel()

	n := &Nodes{Root: t.TempDir()}

	if n.Has(ir.DigestOf([]byte("nowhere"))) {
		t.Error("claimed a node it does not have")
	}

	if _, err := n.Get(ir.DigestOf([]byte("nowhere"))); !errors.Is(err, ErrNotFetched) {
		t.Errorf("a missing node reported %v, want ErrNotFetched", err)
	}
}

// A node whose bytes have rotted is not served.
//
// The same position `StoreSource` takes and for the same reason: this catches an
// honest peer with a bad disk, where the receiver's own check catches a
// dishonest one, and neither is redundant. A store returning wrong bytes is
// detected on read and the read becomes a miss (I4).
func TestARottedNodeIsNotServed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := putNode(t, root, []byte("what it was filed as"))

	if err := os.WriteFile(nodeFile(root, id), []byte("what it is now"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := (&Nodes{Root: root}).Get(id); err == nil {
		t.Error("served bytes that do not hash to the name they are filed under")
	}
}

// Parts serves nodes beside whole layers, and says so through the one interface
// the blob server asks.
func TestPartsServesNodesToo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := putNode(t, root, []byte("held as a node, not as a layer"))

	p := &Parts{Nodes: &Nodes{Root: root}}

	if !p.Has(id) {
		t.Fatal("Parts does not claim a node its store holds")
	}

	if _, err := p.Get(id); err != nil {
		t.Errorf("Parts will not serve a node its store holds: %v", err)
	}
}

// A worker with no node store behaves as it always did.
func TestPartsWithoutNodesIsUnchanged(t *testing.T) {
	t.Parallel()

	p := &Parts{}

	if p.Has(ir.DigestOf([]byte("anything"))) {
		t.Error("a Parts with nothing in it claims something")
	}
}

func putNode(t *testing.T, root string, body []byte) ir.NodeID {
	t.Helper()

	id := ir.DigestOf(body)
	at := nodeFile(root, id)

	if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(at, body, 0o600); err != nil {
		t.Fatal(err)
	}

	return id
}

func nodeFile(root string, id ir.NodeID) string {
	return filepath.Join(root, "nodes", id.String())
}

// The layout this package restates is the one the store uses.
//
// `Nodes.at` spells `<root>/nodes/<id>` for itself rather than importing
// `engine/store`, which would pull the whole layer stack into a package whose
// job is to move bytes. That is a reasonable trade and it is only reasonable
// while something holds the two ends together - otherwise the store moves its
// nodes one day and the fleet serves an empty directory for ever, with nothing
// failing anywhere.
func TestTheNodeLayoutIsTheStoresLayout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.DigestOf([]byte("either end"))

	if got, want := (&Nodes{Root: root}).at(id), store.NodePath(root, id); got != want {
		t.Errorf("fleet files a node at %q and the store reads it at %q"+
			"\n  the fleet would serve nothing and nothing would say so", got, want)
	}
}
