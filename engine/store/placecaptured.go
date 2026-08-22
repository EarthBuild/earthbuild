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
func placeCaptured(store, staging string) (ir.NodeID, error) {
	c, err := layer.TakeOwnedIn(staging, layer.IDMap{}, layer.IDMap{}, nil)
	if err != nil {
		return ir.NodeID{}, fmt.Errorf("capture what was materialised: %w", err)
	}

	layers := filepath.Join(store, "layers")

	err = os.MkdirAll(layers, 0o755)
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

	err = os.Rename(staging, at)
	if err != nil {
		// **Lost a race, which is not a failure.** Two steps on the same base
		// materialise at once, both find the name absent, and the loser's
		// rename lands on a directory that now exists. The winner's copy was
		// named by the same function over the same bytes, so the answer to
		// losing is that the layer is there (the shape `Layers.Put` documents
		// as TOCTOU on a check-then-act, E347).
		if _, again := os.Stat(at); again == nil {
			_ = os.RemoveAll(staging)

			return c.ID, nil
		}

		return ir.NodeID{}, fmt.Errorf("file layer %v: %w", c.ID, err)
	}

	return c.ID, nil
}
