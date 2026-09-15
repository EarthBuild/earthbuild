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
