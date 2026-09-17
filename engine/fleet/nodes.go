package fleet

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Nodes serves a store's content-addressed nodes.
//
// **The gap between what the fleet moves and what a shared cache is made of.**
// Layers and fragments of layers have always crossed; a store's `nodes/` - REAPI
// `Directory` messages, and anything else filed under ℋ over its own bytes - has
// not. A cache shared between machines is exactly those, so without this the
// read-through in `remote.Cache` has nobody to read through to.
//
// Nothing is invented for it. A node is a whole blob, `Held` is the interface
// the blob server already asks, and `earth/blob/1` already carries whole blobs
// verified per chunk (C.4, I2). This is a third place to look, not a third way
// to look.
type Nodes struct {
	// Root is the store root - the same one `Layers` and `Fragments` are given,
	// which is why this is a sibling of theirs rather than a wrapper round one.
	Root string
}

// Has reports whether this worker can serve the whole of a node.
//
// Cheap on purpose: `Has` is what the blob server checks before sending, it is
// asked once per id per request, and a store that read the bytes to answer it
// would read every blob twice.
func (n *Nodes) Has(id ir.NodeID) bool {
	fi, err := os.Lstat(n.at(id))

	return err == nil && fi.Mode().IsRegular()
}

// Get is the node, verified against the name it is filed under.
//
// **A rotted node is not served**, which is `StoreSource`'s position and for its
// reason: this catches an honest peer with a bad disk where the receiver's own
// check catches a dishonest one, and neither is redundant. A store returning
// wrong bytes is detected on read and the read becomes a miss (I4) - so an
// attacker with total control of one can deny service and nothing else.
func (n *Nodes) Get(id ir.NodeID) ([]byte, error) {
	b, err := os.ReadFile(n.at(id)) //nolint:gosec // a path built from a digest
	if err != nil {
		return nil, fmt.Errorf("%w: no node %v here", ErrNotFetched, id)
	}

	if got := ir.DigestOf(b); got != id {
		return nil, fmt.Errorf("%w: node stored as %v hashes to %v", ErrNotFetched, id, got)
	}

	return b, nil
}

// at is where a node lives, which is `store.DirStore`'s layout said again.
//
// Restated rather than imported: `engine/store` depends on `engine/layer`, and
// a fleet that pulled that in for one `filepath.Join` would carry the whole
// layer stack into a package whose job is to move bytes. The layout is one line
// and a test holds both ends of it.
func (n *Nodes) at(id ir.NodeID) string {
	return filepath.Join(n.Root, "nodes", id.String())
}
