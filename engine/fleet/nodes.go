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
// Layers and fragments of layers have always crossed; a store's content-addressed
// blobs - REAPI `Directory` messages under `nodes/`, and everything `blob.Store`
// files beside them - have not. A cache shared between machines is exactly those,
// so without this the read-through in `remote.Cache` has nobody to read through
// to. See places: there are two directories and knowing only one of them made
// this serve nothing a cache is made of.
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
	for _, at := range n.places(id) {
		if fi, err := os.Lstat(at); err == nil && fi.Mode().IsRegular() {
			return true
		}
	}

	return false
}

// Get is the node, verified against the name it is filed under.
//
// **A rotted node is not served**, which is `StoreSource`'s position and for its
// reason: this catches an honest peer with a bad disk where the receiver's own
// check catches a dishonest one, and neither is redundant. A store returning
// wrong bytes is detected on read and the read becomes a miss (I4) - so an
// attacker with total control of one can deny service and nothing else.
func (n *Nodes) Get(id ir.NodeID) ([]byte, error) {
	for _, at := range n.places(id) {
		b, err := os.ReadFile(at) //nolint:gosec // a path built from a digest
		if err != nil {
			continue
		}

		if got := ir.DigestOf(b); got != id {
			return nil, fmt.Errorf("%w: node stored as %v hashes to %v", ErrNotFetched, id, got)
		}

		return b, nil
	}

	return nil, fmt.Errorf("%w: no node %v here", ErrNotFetched, id)
}

// places is where a store keeps something named by ℋ over its own bytes.
//
// **One namespace, two directories, and knowing only one of them made this
// serve nothing that mattered.** `store.NoteNodes` writes REAPI `Directory`
// messages under `nodes/`; `blob.Store` writes everything else - a cache's
// units, a helper's module - under `<first two hex>/<digest>`. Both are content
// addressed and a digest belongs to at most one of them, so looking in both is
// not ambiguity, it is completeness.
//
// Measured rather than reasoned: a worker asking the driver for a pinned helper
// module got "no peer served it" from the one machine that certainly had it,
// because the module was filed at `store/a9/a9be6410…` and looked for at
// `store/nodes/a9be6410…`.
func (n *Nodes) places(id ir.NodeID) [2]string {
	h := id.String()

	return [2]string{
		filepath.Join(n.Root, "nodes", h),
		filepath.Join(n.Root, h[:2], h),
	}
}

// at is where a tree's node lives, which is `store.DirStore`'s layout said
// again.
//
// Restated rather than imported: `engine/store` depends on `engine/layer`, and
// a fleet that pulled that in for one `filepath.Join` would carry the whole
// layer stack into a package whose job is to move bytes. The layout is one line
// and a test holds both ends of it.
func (n *Nodes) at(id ir.NodeID) string {
	return n.places(id)[0]
}
