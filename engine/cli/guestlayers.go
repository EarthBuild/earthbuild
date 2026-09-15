package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// guestPacker is a sandbox that can hand elements of its own store out and take
// them back.
//
// **Because the store may be somewhere the host cannot open.** On macOS it is a
// block device inside the VM by default, and for a correctness reason rather
// than a performance one: APFS is case-insensitive, so two files in a layer
// differing only in case collide on the way in.
type guestPacker interface {
	PackFleetLayer(ctx context.Context, id ir.NodeID, w io.Writer) error
	UnpackFleetLayer(ctx context.Context, r io.Reader) (ir.NodeID, int64, error)
}

// storeHolder is asked which elements the store holds. The same question
// `guestStoreAskers` asks, from the same place.
type storeHolder interface {
	StoreHas(ctx context.Context, ids []ir.NodeID) ([]ir.NodeID, error)
}

// guestLayers is the fleet's view of a store the host cannot open.
//
// **A driver serves the base of every build** (E277), and with the store inside
// the VM it served none of it: `fleet.Layers` reads a host directory, which
// there holds nothing, so a Mac driver held everything a worker needed and
// could offer none of it. Every step was refused for want of something a few
// hundred megabytes away (F4).
//
// Three questions, each already answerable by somebody: what the store holds is
// the executor's to answer, and moving an element either way is the sandbox's -
// through the second exec `SAVE IMAGE` already uses to get a layer out.
type guestLayers struct {
	ctx  context.Context //nolint:containedctx // the build's, for a store that outlives no call
	hold storeHolder
	pack guestPacker
}

// Has reports whether the guest's store holds this element.
//
// **Absence on error, never presence.** A store that cannot be asked has not
// said yes, and reporting a hold this cannot confirm would have the driver
// offer a worker something it may not be able to send - which the worker reads
// as a source that lied rather than as one that was unsure (I11).
func (g *guestLayers) Has(id ir.NodeID) bool {
	held, err := g.hold.StoreHas(g.ctx, []ir.NodeID{id})
	if err != nil {
		return false
	}

	return slices.Contains(held, id)
}

// Get packs one element out of the guest's store.
func (g *guestLayers) Get(id ir.NodeID) ([]byte, error) {
	var buf bytes.Buffer

	err := g.pack.PackFleetLayer(g.ctx, id, &buf)
	if err != nil {
		return nil, fmt.Errorf("pack %v out of the guest's store: %w", id, err)
	}

	return buf.Bytes(), nil
}

// Put files an element a worker produced into the guest's store.
func (g *guestLayers) Put(r io.Reader) (ir.NodeID, int64, error) {
	id, n, err := g.pack.UnpackFleetLayer(g.ctx, r)
	if err != nil {
		return ir.NodeID{}, 0, fmt.Errorf("file an element into the guest's store: %w", err)
	}

	return id, n, nil
}

// fleetStore is where this build's fleet keeps and serves layers.
//
// The host's directory where that is the store, and the guest's where it is
// not. **Asserted rather than assumed**, and the fallback is the old behaviour:
// a sandbox that keeps its store inside and cannot pack it out leaves the fleet
// reading an empty directory, which is what every darwin build did - but it is
// a build that works badly rather than one that does not start.
func fleetStore(sb exec.Sandbox, over any, root string) fleet.Store {
	if !storeInGuest(sb) {
		return &fleet.Layers{Root: root}
	}

	pack, canPack := sb.(guestPacker)
	hold, canAsk := here(over).(storeHolder)

	if !canPack || !canAsk {
		return &fleet.Layers{Root: root}
	}

	return &guestLayers{ctx: context.Background(), hold: hold, pack: pack}
}
