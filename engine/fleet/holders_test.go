package fleet_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The driver passes on where a layer can be fetched from.
//
// The fix for the shape that makes a distributed build slower than one machine:
// if every worker fetches every input from the driver, the driver's uplink is
// the whole fleet's bandwidth and adding machines adds queueing. A worker that
// just produced a layer is the nearest holder of it, and the driver is the only
// party that knows both facts - who produced it, and who needs it next.
func TestTheDriverPassesOnWhereALayerCanBeFetched(t *testing.T) {
	t.Parallel()

	produced := ir.NodeID{7}

	var seen []fleet.Assignment

	f := &fleet.InProcess{}

	f.AddWorker(func(_ context.Context, a fleet.Assignment) (fleet.Reply, error) {
		seen = append(seen, a)

		return fleet.Reply{
			Version: fleet.Version,
			Layer:   produced,
			HeldAt:  "worker-one",
		}, nil
	})

	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: f}

	// The step that makes the layer.
	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	// A later step that needs it as its base.
	_, err = d.Run(t.Context(), delegable(), core.Worker{ID: "w1"},
		[]ir.NodeID{produced}, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("%d assignment(s) reached a worker, want 2", len(seen))
	}

	if !slices.Contains(seen[1].Hints.Holders, "worker-one") {
		t.Errorf("the second step was told holders %v"+
			"\n  the machine that produced its base is the nearest copy, and"+
			" not saying so makes every worker fetch from the driver",
			seen[1].Hints.Holders)
	}

	// And the first step, whose base nobody had produced, is told nothing. An
	// invented holder costs a worker a dial to a machine that has nothing.
	if len(seen[0].Hints.Holders) != 0 {
		t.Errorf("the first step was told holders %v for a base nobody held",
			seen[0].Hints.Holders)
	}
}

// A worker that does not say where it is produces no holder.
//
// `HeldAt` is advisory and a worker may leave it empty - an in-process fleet has
// no address, and one sharing a store has nothing to serve. An empty string
// recorded as a holder would be a dial to nowhere on every later step.
func TestAWorkerWithNoAddressIsNotRecordedAsAHolder(t *testing.T) {
	t.Parallel()

	produced := ir.NodeID{8}

	var seen []fleet.Assignment

	f := &fleet.InProcess{}

	f.AddWorker(func(_ context.Context, a fleet.Assignment) (fleet.Reply, error) {
		seen = append(seen, a)

		return fleet.Reply{Version: fleet.Version, Layer: produced}, nil
	})

	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: f}

	for range 2 {
		_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"},
			[]ir.NodeID{produced}, nil)
		if err != nil {
			t.Fatalf("%v", err)
		}
	}

	for i, a := range seen {
		if len(a.Hints.Holders) != 0 {
			t.Errorf("step %d was told holders %v by a worker that gave no"+
				" address", i, a.Hints.Holders)
		}
	}
}

// Holders cross the wire, canonically.
//
// A hint that did not survive encoding would be a mechanism that worked in
// tests and never in a build - which is the shape E258 was.
func TestHoldersSurviveTheWire(t *testing.T) {
	t.Parallel()

	a := fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Hints:   fleet.Hints{Holders: []string{"one", "two"}},
	}

	got, err := fleet.Decode(fleet.Encode(a))
	if err != nil {
		t.Fatalf("%v", err)
	}

	if !slices.Equal(got.Hints.Holders, a.Hints.Holders) {
		t.Errorf("holders came back as %v, want %v", got.Hints.Holders, a.Hints.Holders)
	}

	// Canonical: the same assignment encodes to the same bytes, hints included.
	if !bytes.Equal(fleet.Encode(a), fleet.Encode(got)) {
		t.Error("re-encoding an assignment gave different bytes (B.1)")
	}
}

// A holder that serves rubbish costs a retry and nothing else.
//
// This is what makes a hint safe to accept from a peer at all (I5): it names
// somewhere to *try*, and every byte from it is verified against the digest that
// was asked for (C.4, E238). A lying holder is therefore a slow build and never
// a wrong one - which is why the driver may pass on an address it has not itself
// checked.
func TestAHolderThatServesRubbishIsSkipped(t *testing.T) {
	t.Parallel()

	body := []byte("the real bytes")

	actual := newMapStore()
	id := putBlob(t, actual, body)

	// A peer that answers for the same digest with something else entirely.
	liar := newMapStore()
	liar.mu.Lock()
	liar.blobs[id] = []byte("not those bytes at all")
	liar.mu.Unlock()

	mine := newMapStore()

	moved, err := fleet.Provision(t.Context(), mine,
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{id}},
		&fleet.LayerSource{Label: "liar", Held: liar},
		&fleet.LayerSource{Label: "driver", Held: actual})
	if err != nil {
		t.Fatalf("a lying holder failed the build instead of costing a retry: %v", err)
	}

	got, err := mine.Get(id)
	if err != nil || !bytes.Equal(got, body) {
		t.Errorf("the store ended up with %q", got)
	}

	if moved.Bytes != int64(len(body)) {
		t.Errorf("accounted %d bytes for one blob of %d", moved.Bytes, len(body))
	}
}

// A worker fetches from the peer it was pointed at, before the driver.
//
// The whole mechanism, end to end on the worker's side: the hint names a
// machine, the worker dials it, and the driver is never asked. That last part is
// the point - if the driver is asked anyway, the mesh is a star with extra
// steps.
func TestAWorkerFetchesFromThePeerItWasPointedAt(t *testing.T) {
	t.Parallel()

	body := []byte("a layer another worker just produced")

	peer := newMapStore()
	id := putBlob(t, peer, body)

	// The driver does not have it. In a real fleet it would, but then a worker
	// that ignored the hint would still pass this test.
	driver := &countingSource{LayerSource: &fleet.LayerSource{
		Label: "driver", Held: newMapStore(),
	}}

	dialled := []string{}

	run := fleet.Runner(&countingLocal{}, core.Worker{ID: "w2"},
		fleet.WithBlobs(newMapStore(), driver),
		fleet.WithPeers("worker-two", func(at string) (fleet.Source, error) {
			dialled = append(dialled, at)

			return &fleet.LayerSource{Label: at, Held: peer}, nil
		}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{Holders: []string{"worker-one"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused != "" {
		t.Fatalf("refused: %s", reply.Refused)
	}

	if !slices.Contains(dialled, "worker-one") {
		t.Errorf("dialled %v; the holder it was pointed at was never asked", dialled)
	}

	if driver.batches != 0 {
		t.Error("the driver was asked anyway" +
			"\n  a mesh that still routes every blob through the driver is a" +
			" star with extra steps")
	}

	// And it announces itself, so the next step needing what it produces is
	// pointed here rather than at the driver.
	if reply.HeldAt != "worker-two" {
		t.Errorf("the reply announces %q; a worker that does not say where it"+
			" is cannot be fetched from", reply.HeldAt)
	}
}

// A holder that cannot be dialled is skipped, not fatal.
//
// The address came from a hint, which came from another machine's claim about
// itself. It may be stale, wrong, or unreachable from here - none of which is a
// reason to fail a step whose bytes the driver still has.
func TestAHolderThatCannotBeDialledIsSkipped(t *testing.T) {
	t.Parallel()

	body := []byte("still available from the driver")

	driver := newMapStore()
	id := putBlob(t, driver, body)

	mine := newMapStore()

	run := fleet.Runner(&countingLocal{}, core.Worker{ID: "w2"},
		fleet.WithBlobs(mine, &fleet.LayerSource{Held: driver}),
		fleet.WithPeers("", func(string) (fleet.Source, error) {
			return nil, errors.New("no route to that machine")
		}))

	reply, err := run(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
		Base:    []ir.NodeID{id},
		Hints:   fleet.Hints{Holders: []string{"gone-away"}},
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	if reply.Refused != "" {
		t.Fatalf("a stale holder hint failed the step: %s", reply.Refused)
	}

	if !mine.Has(id) {
		t.Error("the blob never arrived, though the driver had it")
	}
}

// Holders reach a worker across a real connection.
//
// **The hop nothing tested.** That a driver names holders is tested against an
// in-process fleet; that they survive encoding is tested against a buffer; that
// a worker dials one is tested with a fake. The composition of all three - a
// real `Rendezvous`, a real `Join`, a real `Runner` - was not, and a
// two-machine run reported a worker with no sources at all (E309, E310).
//
// Every part tested and the assembly not, which is E258's shape for the fifth
// time.
func TestHoldersReachAWorkerAcrossARealConnection(t *testing.T) {
	t.Parallel()

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)
	session := fleet.Session{Session: "holders", RunID: "1", Attempt: 1, Repo: "r"}
	secret := []byte("shared")

	driver, err := fleet.BindDriver(t.Context(), session, secret, iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = driver.Shutdown(context.WithoutCancel(t.Context())) })

	r := &fleet.Rendezvous{Reach: 20 * time.Second}

	go func() { _ = r.Accept(t.Context(), driver, func(err error) { t.Logf("driver: %v", err) }) }()

	id, err := fleet.DriverID(session, secret)
	if err != nil {
		t.Fatal(err)
	}

	w, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = w.Shutdown(context.WithoutCancel(t.Context())) })

	told := make(chan []string, 4)

	go func() {
		_ = fleet.Join(t.Context(), w,
			netaddr.NewEndpointAddr(id).WithIP(driver.LocalAddr()),
			fleet.Runner(&countingLocal{}, core.Worker{ID: "w1"},
				// A store and a base, or the worker provisions nothing and
				// never dials anybody - which is what the first version of this
				// test measured.
				fleet.WithBlobs(newMapStore()),
				fleet.WithPeers("", func(at string) (fleet.Source, error) {
					// Every holder the worker was told about arrives here.
					select {
					case told <- []string{at}:
					default:
					}

					return nil, errNoBase
				})),
			func(err error) { t.Logf("worker: %v", err) })
	}()

	for deadline := time.Now().Add(20 * time.Second); r.Workers() == 0 &&
		time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}

	if r.Workers() == 0 {
		t.Skip("no worker joined")
	}

	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: r, Self: "the-driver"}

	// A base it does not have, so it has to go looking.
	_, err = d.Run(t.Context(), delegable(), core.Worker{ID: "w1"},
		[]ir.NodeID{{9}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-told:
		if !slices.Contains(got, "the-driver") {
			t.Errorf("the worker was told holders %v"+
				"\n  the driver names itself, and a worker with no holders has"+
				" nowhere to fetch from", got)
		}

	case <-time.After(20 * time.Second):
		t.Fatal("the worker never ran the step")
	}
}
