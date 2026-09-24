package fleet_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// The driver fetches from a worker over the connection the worker opened.
//
// **The case that a firewall makes normal.** A worker dials out and the driver
// answers; the reverse never works, so a driver that pulls a result by dialling
// the worker cannot reach it - which is what a real two-machine run found, on a
// machine that simply had a firewall on (E277, E278).
//
// QUIC is bidirectional, and this engine already relies on that: assignments
// travel to a worker down a connection the worker made. Blobs can travel the
// same way, and then a worker needs nothing reachable at all - no port, no
// forwarding, no relay (E279).
func TestTheDriverFetchesOverTheConnectionAWorkerOpened(t *testing.T) {
	t.Parallel()

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)
	session := fleet.Session{Session: "back", RunID: "1", Attempt: 1, Repo: "r"}
	secret := []byte("shared")

	theirs := t.TempDir()
	id := aLayer(t, theirs)

	driver, err := fleet.BindDriver(t.Context(), session, secret, iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = driver.Shutdown(context.WithoutCancel(t.Context())) })

	r := &fleet.Rendezvous{Reach: 20 * time.Second}

	go func() { _ = r.Accept(t.Context(), driver, func(err error) { t.Logf("driver: %v", err) }) }()

	want, err := fleet.DriverID(session, secret)
	if err != nil {
		t.Fatal(err)
	}

	w, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = w.Shutdown(context.WithoutCancel(t.Context())) })

	// The worker serves its store over the connection it makes, and listens on
	// nothing. There is no blob endpoint here at all: if the driver reaches this
	// layer, it reached it the only way that works behind a firewall.
	go func() {
		_ = fleet.Join(t.Context(), w,
			netaddr.NewEndpointAddr(want).WithIP(driver.LocalAddr()),
			func(context.Context, fleet.Assignment) (fleet.Reply, error) {
				return fleet.Reply{Version: fleet.Version, Layer: id, HeldAt: "unreachable"}, nil
			},
			func(err error) { t.Logf("worker: %v", err) },
			fleet.Serving(&fleet.Layers{Root: theirs}))
	}()

	for deadline := time.Now().Add(20 * time.Second); r.Workers() == 0 &&
		time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}

	if r.Workers() == 0 {
		t.Skip("no worker joined")
	}

	// The driver has to run one assignment first, because that is how it learns
	// which connection holds what.
	_, err = r.Assign(t.Context(), fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
	})
	if err != nil {
		t.Fatalf("assigning: %v", err)
	}

	src, ok := r.SourceFor("unreachable")
	if !ok {
		t.Fatal("the driver does not know it can reach that worker at all")
	}

	mine := &fleet.Layers{Root: t.TempDir()}

	moved, err := fleet.Provision(t.Context(), mine,
		fleet.Assignment{Version: fleet.Version, Base: []ir.NodeID{id}}, src)
	if err != nil {
		t.Fatalf("fetching over the worker's own connection: %v", err)
	}

	if !mine.Has(id) {
		t.Fatal("the layer did not arrive")
	}

	got, err := layer.Take(mine.Root + "/layers/" + id.String())
	if err != nil {
		t.Fatal(err)
	}

	if got.ID != id {
		t.Errorf("what arrived is %v, filed as %v", got.ID, id)
	}

	if moved.Bytes == 0 {
		t.Error("a layer crossed a connection and was accounted as free")
	}
}
