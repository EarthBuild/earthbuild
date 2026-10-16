package fleet_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A build over a real fleet produces what a local build produces.
//
// E236 proved this in one process, which tested the *path* -
// `Delegate` -> `Assign` -> `Reply` -> `resultOf` -> scheduler. This is the same
// claim with a network in the middle: two endpoints, a worker that dialled in,
// QUIC carrying the canonical encoding, and a scheduler that does not know any
// of that is happening.
//
// The claim delegation has to earn is not that a message is well-formed. It is
// that the answer is the same on the other side, and a fleet that produced
// different layers would be a correctness failure wearing a performance
// feature's clothes.
func TestABuildOverARealFleetMatchesALocalBuild(t *testing.T) {
	t.Parallel()

	const steps = 4

	// Local, for comparison.
	solo := &building{name: "local"}
	local := &core.Scheduler{
		Workers:  []core.Worker{{ID: "me", IsInvoker: true}},
		Executor: solo,
		Cache:    &memCache{},
		Blobs:    everyBlob{},
		Writer:   "test",
	}

	_, err := local.Run(t.Context(), chain(steps))
	if err != nil {
		t.Fatal(err)
	}

	// A driver whose identity is the session's, and a worker that derives it.
	session := fleet.Session{Session: "build", RunID: "1", Attempt: 1, Repo: "r"}
	secret := []byte("shared")

	addr := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	driver, err := fleet.BindDriver(context.Background(), session, secret,
		iroh.WithBindAddr(addr))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	r := &fleet.Rendezvous{}

	go func() { _ = r.Accept(t.Context(), driver, func(err error) { t.Logf("driver: %v", err) }) }()

	w, err := iroh.Bind(context.Background(), iroh.WithBindAddr(addr))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = w.Shutdown(context.Background()) })

	id, err := fleet.DriverID(session, secret)
	if err != nil {
		t.Fatal(err)
	}

	remote := &building{name: "remote"}

	go func() {
		_ = fleet.Join(t.Context(), w, netaddr.NewEndpointAddr(id).WithIP(driver.LocalAddr()),
			func(ctx context.Context, a fleet.Assignment) (fleet.Reply, error) {
				n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: a.Op.Args}}

				res, runErr := remote.Run(ctx, n, core.Worker{ID: "w1"}, a.Base, a.Sources)
				if runErr != nil {
					return fleet.Reply{}, runErr
				}

				return fleet.Reply{
					Version: fleet.Version, Layer: res.Layer, Content: res.Content,
					Observation: fleet.Observation{Reads: res.Observation.Reads},
				}, nil
			},
			func(err error) { t.Logf("worker: %v", err) })
	}()

	for deadline := time.Now().Add(10 * time.Second); r.Workers() == 0 &&
		time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}

	if r.Workers() == 0 {
		t.Fatal("no worker joined")
	}

	here := &building{name: "here"}

	fleeted := &core.Scheduler{
		Workers:  []core.Worker{{ID: "me", IsInvoker: true}, {ID: "w1"}},
		Executor: &fleet.Delegating{Local: here, Fleet: r},
		Cache:    &memCache{},
		Blobs:    everyBlob{},
		Writer:   "test",
	}

	_, err = fleeted.Run(t.Context(), chain(steps))
	if err != nil {
		t.Fatal(err)
	}

	// **Without this the comparison is between two local builds** - the shape of
	// a green gate over a feature that is not running (E90, E236).
	if remote.count() == 0 {
		t.Fatalf("nothing crossed the network (%d ran here)", here.count())
	}

	t.Logf("%d steps ran on the worker, %d here", remote.count(), here.count())

	want, got := local.Record.Steps, fleeted.Record.Steps

	if len(want) != len(got) {
		t.Fatalf("the two builds recorded %d and %d steps", len(want), len(got))
	}

	for i := range want {
		if want[i].Layer != got[i].Layer {
			t.Errorf("step %d produced %v locally and %v over the fleet"+
				"\n  a network in the middle must change nothing",
				i, want[i].Layer, got[i].Layer)
		}
	}
}
