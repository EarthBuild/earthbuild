package store_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// The empty blob is always here, and is never asked for.
//
// **Every peer assumes it and none of them sends it.** An action with no inputs
// has an empty Directory as its input root, whose digest is the hash of no
// bytes; a client does not upload something it takes to be universal, and a
// store that has never been told about it refuses to materialise anything over
// it. Buck2 fails with "the input root ... is not in this store" naming the
// hash of nothing, which reads like a lost upload and is not one.
//
// Not written down, because there is nothing to write: it is knowable from the
// digest function alone, and a file holding no bytes would be a thing to
// collect, lose, and be surprised by.
func TestTheEmptyBlobIsAlwaysHeld(t *testing.T) {
	// Not parallel: SelectHashForTest changes a process-wide choice.
	restore := ir.SelectHashForTest(t, ir.HashSHA256)
	defer restore()

	st := store.DirStore(t.TempDir())
	empty := ir.DigestOf(nil)

	got, err := st.Node(empty)
	if err != nil {
		t.Fatalf("a store that has never seen a build cannot produce the empty blob: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("the empty blob is %d bytes", len(got))
	}

	// And a client is never told to send it, because it would have nothing to
	// send and the round trip is the whole cost.
	if missing := st.MissingNodes([]ir.NodeID{empty}); len(missing) != 0 {
		t.Errorf("a client was asked to upload the empty blob: %v", missing)
	}

	// The other digest function has its own empty digest, and this must not be
	// a hardcoded constant that is right for one of them.
	restore()

	restoreB := ir.SelectHashForTest(t, ir.HashBLAKE3)
	defer restoreB()

	if _, err := store.DirStore(t.TempDir()).Node(ir.DigestOf(nil)); err != nil {
		t.Errorf("the empty blob is not held under %v: %v", ir.Hash(), err)
	}
}
