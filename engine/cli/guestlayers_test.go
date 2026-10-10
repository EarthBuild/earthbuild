package cli

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// asksAndPacks is a sandbox-plus-executor that can do both halves.
type asksAndPacks struct {
	held []ir.NodeID
	err  error
}

func (a *asksAndPacks) StoreHas(_ context.Context, ids []ir.NodeID) ([]ir.NodeID, error) {
	if a.err != nil {
		return nil, a.err
	}

	var out []ir.NodeID

	for _, id := range ids {
		for _, h := range a.held {
			if h == id {
				out = append(out, id)
			}
		}
	}

	return out, nil
}

func (a *asksAndPacks) PackFleetLayer(context.Context, ir.NodeID, io.Writer) error { return nil }

func (a *asksAndPacks) UnpackFleetLayer(context.Context, io.Reader) (ir.NodeID, int64, error) {
	return ir.NodeID{}, 0, nil
}

// TestAStoreInTheGuestIsAskedOfTheGuest.
//
// **A driver serves the base of every build** (E277), and a driver whose store
// is inside the VM served none of it: `fleet.Layers` reads a host directory,
// which on macOS holds nothing, so a worker was refused every step for want of
// something the driver was holding. The default there is store-in-VM and it is
// the *correct* setting - APFS is case-insensitive and layers collide on the
// shared mount - so this is not an exotic configuration, it is the only one a
// Mac should be using (F4).
func TestAStoreInTheGuestIsAskedOfTheGuest(t *testing.T) {
	t.Parallel()

	var want ir.NodeID
	want[0] = 7

	g := &guestLayers{
		ctx:  t.Context(),
		hold: &asksAndPacks{held: []ir.NodeID{want}},
		pack: &asksAndPacks{},
	}

	if !g.Has(want) {
		t.Error("an element the guest holds was reported absent, so the driver" +
			" offers a worker nothing and the worker refuses the step")
	}

	var other ir.NodeID
	other[0] = 9

	if g.Has(other) {
		t.Error("an element nothing holds was reported present")
	}
}

// TestAStoreThatCannotBeAskedHoldsNothing.
//
// **Absence on error, never presence.** Reporting a hold this cannot confirm
// has the driver offer a worker something it may not be able to send, and the
// worker reads that as a source that lied rather than one that was unsure
// (I11).
func TestAStoreThatCannotBeAskedHoldsNothing(t *testing.T) {
	t.Parallel()

	var id ir.NodeID
	id[0] = 7

	g := &guestLayers{
		ctx:  t.Context(),
		hold: &asksAndPacks{held: []ir.NodeID{id}, err: errors.New("the guest is gone")},
		pack: &asksAndPacks{},
	}

	if g.Has(id) {
		t.Error("a store that could not be asked reported that it holds something")
	}
}

// guestLayers is what the fleet wants of a store, so that the wiring cannot
// drift from the interface it feeds.
var _ fleet.Store = (*guestLayers)(nil)

// declares is a sandbox that can say what an element declares.
type declares struct{ has map[ir.NodeID]bool }

func (d *declares) ReadDeclaration(_ context.Context, id ir.NodeID) ([]byte, bool, error) {
	if d.has[id] {
		return []byte("EBDECL1"), true, nil
	}

	return nil, false, nil
}

// TestADeclarationIsSomethingTheStoreHolds.
//
// **`StoreHas` answers about layers.** It stats a layer directory, and a stack
// element contributing only configuration - environment, working directory,
// user, entrypoint - is a file beside those directories. So a driver reported
// not holding one, offered no source for it, and every worker refused every
// step standing on it:
//
//	1 of 4 input(s) for a delegated step: some blobs could not be fetched
//	  first 5623a794…, and no source was consulted at all
//
// The same defect the fleet's own store had this morning, one level down: the
// element that is not a layer is the one that keeps being forgotten (E-F1).
func TestADeclarationIsSomethingTheStoreHolds(t *testing.T) {
	t.Parallel()

	var onlyDeclared ir.NodeID
	onlyDeclared[0] = 5

	g := &guestLayers{
		ctx:  t.Context(),
		hold: &asksAndPacks{},
		pack: &asksAndPacks{},
		decl: &declares{has: map[ir.NodeID]bool{onlyDeclared: true}},
	}

	if !g.Has(onlyDeclared) {
		t.Error("an element held as a declaration was reported absent, so no" +
			" source is offered and every step standing on it is refused")
	}

	var neither ir.NodeID
	neither[0] = 6

	if g.Has(neither) {
		t.Error("an element nothing holds was reported present")
	}

	// A sandbox that cannot be asked serves layers and not declarations, which
	// is worse than this and better than refusing to start.
	blind := &guestLayers{ctx: t.Context(), hold: &asksAndPacks{}, pack: &asksAndPacks{}}
	if blind.Has(onlyDeclared) {
		t.Error("a sandbox with no way to answer claimed to hold a declaration")
	}
}
