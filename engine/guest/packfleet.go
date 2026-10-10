package guest

import (
	"fmt"
	"io"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// PackFleetLayer writes one element of this guest's store in the fleet's pack
// format.
//
// **The other host reader the store moving into the VM took away.** `PackLayer`
// beside this carries a layer out as an OCI blob, which is what `SAVE IMAGE`
// needs; a fleet speaks a different pack, and its blob server reads a host
// directory that on macOS holds nothing at all. So a Mac cannot serve the base
// of its own build to a worker, and every step is refused for want of something
// the driver is holding a few hundred megabytes away (F4).
//
// **An element, not a layer.** `fleet.Layers.Get` answers for a declaration as
// well as a tree, and a declaration is exactly the element that was missing
// when this was found (E-F1).
//
// Packed by `fleet.Layers` rather than by a second encoder for the same wire
// format: a pack this writes has to be one a worker can unpack, and two
// implementations of one format is the arrangement that guarantees they diverge
// eventually.
func PackFleetLayer(root string, id ir.NodeID, w io.Writer) error {
	body, err := (&fleet.Layers{Root: root}).Get(id)
	if err != nil {
		return fmt.Errorf("pack %s for the fleet: %w", id, err)
	}

	_, err = w.Write(body)
	if err != nil {
		return fmt.Errorf("write the pack for %s: %w", id, err)
	}

	return nil
}

// UnpackFleetLayer files an element a peer sent into this guest's store.
//
// The return journey of `PackFleetLayer`, and the reason it exists is the same:
// a driver whose store is inside the VM has to take back what a worker produced
// (E274), and the host cannot write into that store any more than it can read
// it.
//
// **The identity is derived here and not taken from the sender**, because
// `fleet.Layers.Put` derives it: what arrives is captured and named by its
// contents, and `Provision` refuses anything whose name is not the one it asked
// for. A guest is no more trusting of a stream than a worker is (I6, §5.3).
func UnpackFleetLayer(root string, r io.Reader) (ir.NodeID, int64, error) {
	id, n, err := (&fleet.Layers{Root: root}).Put(r)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("take an element for the fleet: %w", err)
	}

	return id, n, nil
}
