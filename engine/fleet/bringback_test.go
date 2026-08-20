package fleet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// needing is a local executor that insists on having its base.
//
// Which every real one does: it materialises the base before running the step,
// and a base that is not in this machine's store is a step that cannot start.
type needing struct {
	store *mapStore
	ran   int
	miss  []ir.NodeID
}

var errNoBase = errors.New("the base is not on this machine")

func (n *needing) Run(
	_ context.Context, _ *ir.Node, _ core.Worker, base []ir.NodeID, sources [][]ir.NodeID,
) (core.Result, error) {
	for _, stack := range append([][]ir.NodeID{base}, sources...) {
		for _, id := range stack {
			if !n.store.Has(id) {
				n.miss = append(n.miss, id)

				return core.Result{}, errNoBase
			}
		}
	}

	n.ran++

	return core.Result{Layer: ir.NodeID{9}}, nil
}

// A step that must run here can use what a worker produced.
//
// The hole this looks for is E258's, one direction further on. A delegated step
// leaves its layer **on the worker**, and the driver holds a digest and nothing
// else. Anything that then has to run on the invoking machine - a `host` step, a
// construct no worker implements, an artifact being written out - needs those
// bytes here, and there was no path that brought them.
//
// The symptom is not a wrong build. It is a build that fails at the last step,
// having done all the work, with a base that exists on a machine nobody asked.
func TestAStepThatMustRunHereCanUseWhatAWorkerProduced(t *testing.T) {
	t.Parallel()

	body := []byte("what the worker produced")

	// The worker's store, reachable as a peer. Filed under its own digest,
	// because that is what a store does - a fake that lets the caller choose the
	// name is the conflation E261 was built on.
	theirs := newMapStore()
	made := putBlob(t, theirs, body)

	here := newMapStore()
	local := &needing{store: here}

	f := &fleet.InProcess{}

	f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{Version: fleet.Version, Layer: made, HeldAt: "them"}, nil
	})

	d := &fleet.Delegating{
		Local: local,
		Fleet: f,
		Store: here,
		Peers: func(string) (fleet.Source, error) {
			return &fleet.LayerSource{Label: "them", Held: theirs}, nil
		},
	}

	// A delegated step, which leaves its layer over there.
	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatalf("delegating: %v", err)
	}

	// And a step that has to run here, on that layer.
	host := &ir.Node{
		Op:   ir.Op{Kind: ir.OpHost, Args: []string{"cp"}},
		Meta: ir.Meta{Source: "Earthfile:9"},
	}

	_, err = d.Run(t.Context(), host, core.Worker{ID: "me", IsInvoker: true},
		[]ir.NodeID{made}, nil)
	if err != nil {
		t.Fatalf("a step that must run here could not: %v"+
			"\n  its base is on a worker, and nothing brought it back", err)
	}

	if local.ran != 1 {
		t.Errorf("the local step ran %d time(s); missing %v", local.ran, local.miss)
	}

	if !here.Has(made) {
		t.Error("the layer never reached this machine")
	}
}

// A step whose inputs nobody delegated costs nothing to run here.
//
// The overwhelmingly common case, and the reason this is keyed on what the
// driver *knows* a worker produced rather than on what the local store happens
// to be missing: a host step at the start of a build, on a fleet that has done
// nothing yet, must not open a connection to discover there is nothing to fetch.
func TestAStepWithNothingDelegatedBehindItDialsNobody(t *testing.T) {
	t.Parallel()

	here := newMapStore()
	local := &needing{store: here}

	base := putBlob(t, here, []byte("made right here"))

	dialled := 0

	d := &fleet.Delegating{
		Local: local,
		Fleet: &fleet.InProcess{},
		Store: here,
		Peers: func(string) (fleet.Source, error) {
			dialled++

			return nil, errNoBase
		},
	}

	host := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"cp"}}}

	_, err := d.Run(t.Context(), host, core.Worker{ID: "me", IsInvoker: true},
		[]ir.NodeID{base}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if dialled != 0 {
		t.Errorf("dialled %d peer(s) for a build that has delegated nothing",
			dialled)
	}

	if local.ran != 1 {
		t.Errorf("the step ran %d time(s)", local.ran)
	}
}

// The driver names itself as a holder of last resort.
//
// It holds the base of every build - the first thing every worker needs - and a
// worker that cannot reach a peer needs somewhere to fall back to. Naming itself
// in the hint means a worker needs no configuration of its own to find the
// driver's blobs: the address arrives with the work (E277).
//
// **Last**, after every peer. A driver that put itself first would be the star
// topology E260 exists to avoid, arrived at from the other end.
func TestTheDriverNamesItselfLastAmongHolders(t *testing.T) {
	t.Parallel()

	made := ir.NodeID{7}

	var seen []fleet.Assignment

	f := &fleet.InProcess{}

	f.AddWorker(func(_ context.Context, a fleet.Assignment) (fleet.Reply, error) {
		seen = append(seen, a)

		return fleet.Reply{Version: fleet.Version, Layer: made, HeldAt: "worker-one"}, nil
	})

	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: f, Self: "the-driver"}

	// One step to make a layer somebody holds.
	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A second that needs it.
	_, err = d.Run(t.Context(), delegable(), core.Worker{ID: "w1"},
		[]ir.NodeID{made}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(seen) != 2 {
		t.Fatalf("%d assignment(s), want 2", len(seen))
	}

	// The first step needed nothing, and is still told where the driver is: a
	// worker with an empty store needs the base of the build, and nobody else
	// has it yet.
	if got := seen[0].Hints.Holders; len(got) != 1 || got[0] != "the-driver" {
		t.Errorf("the first step was told %v, want just the driver", got)
	}

	got := seen[1].Hints.Holders
	if len(got) != 2 || got[0] != "worker-one" || got[1] != "the-driver" {
		t.Errorf("the second step was told %v"+
			"\n  the peer holding its base first, the driver last - a driver"+
			" that put itself first is the star topology from the other end", got)
	}
}

// A layer that cannot be brought back is reported as missing, not as broken.
//
// The scheduler answers `MissingInput` by rebuilding whatever made the layer
// (E278); it answers a plain error by failing the build. So the distinction is
// the whole difference between a fleet that degrades and a fleet that is a
// single point of failure, and it has to be made here - the driver is the party
// that knows a layer was somewhere and is not reachable.
func TestALayerThatCannotBeBroughtBackIsReportedAsMissing(t *testing.T) {
	t.Parallel()

	made := ir.NodeID{7}

	f := &fleet.InProcess{}

	f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{Version: fleet.Version, Layer: made, HeldAt: "gone-away"}, nil
	})

	here := newMapStore()

	d := &fleet.Delegating{
		Local: &needing{store: here},
		Fleet: f,
		Store: here,
		Peers: func(string) (fleet.Source, error) {
			return nil, errNoBase // the machine is unreachable
		},
	}

	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	host := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"cp"}}}

	_, err = d.Run(t.Context(), host, core.Worker{ID: "me", IsInvoker: true},
		[]ir.NodeID{made}, nil)
	if err == nil {
		t.Fatal("a step ran without a base nobody could supply")
	}

	var missing core.MissingInput
	if !errors.As(err, &missing) {
		t.Fatalf("%v\n  reported as a failure rather than as a layer that has"+
			" to be made again; the scheduler can only act on the second", err)
	}

	if missing.Layer != made {
		t.Errorf("named %v as missing, want %v", missing.Layer, made)
	}

	if missing.Where != "gone-away" {
		t.Errorf("said it was at %q; naming the machine is what makes the"+
			" failure diagnosable", missing.Where)
	}
}
