package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// squashInto merges a range of layers into one, oldest first.
//
// Separate from the method because everything interesting here is filesystem
// work over a directory, and a test that had to stand up a sandbox to check
// which of two files wins would be testing the sandbox.
//
// Hard links rather than copies. A layer is immutable once written - that is
// what makes it addressable - so a squash of ten gigabytes costs inodes and no
// bytes. The link farm the mount uses relies on the same property.
func squashInto(ctx context.Context, store string, into ir.NodeID, rng []ir.NodeID) error {
	final := LayerStore(store).Path(into)

	// Already built, by this build or a previous one: the identity is derived
	// from the range, so there is nothing here that could be out of date.
	_, err := os.Stat(final)
	if err == nil {
		return nil
	}

	// Beside the final name, and arriving by rename. Other steps of this build
	// are reading the store at this moment and a directory that exists is a
	// directory that will be mounted, so a half-merged one must never be
	// reachable by the name a mount would use.
	partial := final + ".squashing"

	err = os.RemoveAll(partial)
	if err != nil {
		return fmt.Errorf("clear a previous attempt at %s: %w", partial, err)
	}

	// Staging inside the store, so private: nothing outside the engine reads
	// a half-built layer, and its mode is not part of what the build made.
	err = os.MkdirAll(partial, 0o750)
	if err != nil {
		return fmt.Errorf("prepare the squashed layer: %w", err)
	}

	defer os.RemoveAll(partial)

	for _, id := range rng {
		err := ctx.Err()
		if err != nil {
			return err
		}

		src := filepath.Join(store, "layers", id.String())

		_, err = os.Stat(src)
		if err != nil {
			return fmt.Errorf("layer %s is named in a stack and is not in the store: %w",
				id.String(), err)
		}

		// Oldest first, so a later layer's version of a file lands last and
		// wins - which is what the mount this replaces would have done.
		err = LinkTree(src, partial)
		if err != nil {
			return fmt.Errorf("merge layer %s: %w", id.String(), err)
		}
	}

	// A concurrent squash of the same range getting there first is a success:
	// the identity says the two results are the same bytes. Said once, in
	// `Publish`, for every caller that files a layer.
	return Publish(store, into, partial)
}

// MountableStackDepth is the deepest stack the guest can actually mount.
//
// Not overlayfs's limit, which is 500 layers, and not MaxStackDepth, which
// describes that limit. `mount(2)` reads its options from a single page, and a
// layer named by a 64-character digest under the guest's store costs 98 bytes
// of it - so the mount fails at about 41 layers by full name and about 90 with
// the short-name farm the materialiser uses (E49).
//
// 64 rather than 90: the arithmetic depends on where the store is, the farm
// falls back to full paths on a name clash, and a stack that has to be
// flattened one step sooner than strictly necessary costs one squash, while one
// flattened one step too late costs the build. The mount refuses anything that
// still does not fit, so this number being wrong is slow rather than fatal.
const MountableStackDepth = 64
