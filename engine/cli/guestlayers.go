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

// declReader is a sandbox that can say what an element declares.
//
// **Because `StoreHas` answers about layers.** It asks `store.DirStore.Has`,
// which stats a layer directory, and a stack element held as a declaration is a
// file beside those directories - so a driver reported that it did not hold one
// and no source was offered for it. That is the same defect the fleet's own
// store had this morning, one level down (E-F1).
type declReader interface {
	ReadDeclaration(ctx context.Context, id ir.NodeID) ([]byte, bool, error)
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
	// decl answers for the elements `hold` does not know about. Nil where the
	// sandbox cannot be asked, and then a declaration is simply not served -
	// which is what every darwin build did before this.
	decl declReader
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

	if slices.Contains(held, id) {
		return true
	}

	// **A stack element need not be a layer.** `StoreHas` stats a layer
	// directory, and an image contributing only configuration is a file beside
	// those - so this reported not holding one, offered no source, and every
	// worker refused every step standing on it. Asked second because it costs
	// an exec and almost every element is a tree (E-F1).
	if g.decl == nil {
		return false
	}

	_, declared, err := g.decl.ReadDeclaration(g.ctx, id)

	return err == nil && declared
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
		// **And the nodes beside the layers**, which is where a shared cache
		// lives. A worker has served its own since `fleet.Nodes` existed; a
		// driver served only layers, and the driver is the machine holding the
		// helper module every worker has to run and the units of every cache it
		// has filled. A worker asking for one got "no peer served it" from the
		// one peer that certainly had it.
		return fleet.WithNodes(&fleet.Layers{Root: root}, root)
	}

	// **Not when the store is on the guest's device.** `nodes/` is then inside
	// the VM and a reader rooted at the host's path would claim nothing and
	// serve nothing - honestly, but a cache that crosses on Linux and silently
	// does not on a Mac is worse than one that does neither. Sharing from a
	// guest-side store is E511's gap and is not closed here.

	pack, canPack := sb.(guestPacker)
	hold, canAsk := here(over).(storeHolder)

	if !canPack || !canAsk {
		return &fleet.Layers{Root: root}
	}

	// Optional: a sandbox that cannot be asked serves layers and not
	// declarations, which is worse than this and better than nothing.
	reader, _ := sb.(declReader)

	return &guestLayers{
		ctx: context.Background(), hold: hold, pack: pack, decl: reader,
	}
}
