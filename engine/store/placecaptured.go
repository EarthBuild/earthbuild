package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// placeCaptured files a materialised tree under the digest of its contents, and
// returns that digest.
//
// **§3.2: a layer is a content-addressed filesystem delta.** A layer filed under
// the node id of the operation that produced it is filed under a name for the
// *derivation*, which a peer cannot check what it receives against - so a fleet
// refuses every base it is sent and each machine fetches its own (E507).
//
// RUN results were already filed this way, because a capture returns the digest
// and the executor stored what it returned. Images and staged contexts were not.
// That is why some transfers between machines worked and bases never did, and
// why the two halves of one store disagreed about what a layer's name means.
//
// The tree is captured with identity maps, matching `Layers.Put` at the far end
// of a transfer: a name computed one way here and another way there is a layer
// that cannot survive the trip, whichever way is right.
//
// Already present is success, not a collision: the same contents have the same
// name, and whichever copy is there has been named by exactly this function. The
// staging tree is removed rather than filed twice.
func placeCaptured(store, staging string, p Placement) (ir.NodeID, error) {
	c, err := layer.TakeOwnedKnowing(staging, layer.IDMap{}, layer.IDMap{}, p.Owners, p.Digests)
	if err != nil {
		return ir.NodeID{}, fmt.Errorf("capture what was materialised: %w", err)
	}

	layers := filepath.Join(store, "layers")

	// 0o750, as every other directory this store makes is - `place.go`,
	// `index.go`, `store.go` and `exportmemo.go` all agree, and this one line
	// did not (gosec G301). The store root above it is already 0o750, so the
	// looser mode bought nothing but an inconsistency.
	err = os.MkdirAll(layers, 0o750)
	if err != nil {
		return ir.NodeID{}, fmt.Errorf("prepare the layer store: %w", err)
	}

	at := filepath.Join(layers, c.ID.String())

	_, err = os.Stat(at)
	if err == nil {
		// Somebody has already filed these bytes under this name. Removing the
		// second copy is the whole of what "already there" costs.
		_ = os.RemoveAll(staging)

		return c.ID, nil
	}

	// Losing the race to file it is not a failure - see `Publish`, which is
	// where that is said.
	err = Publish(store, c.ID, staging)
	if err != nil {
		return ir.NodeID{}, err
	}

	// Nothing on the winning path: the rename took the staging tree away, and
	// removing what is not there succeeds. On the losing path it is still
	// here, and this is what "already filed" costs.
	_ = os.RemoveAll(staging)

	return c.ID, nil
}
