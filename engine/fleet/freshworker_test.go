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

// A worker that has just joined can be given work.
//
// Placement refuses a worker that has not declared a platform, and a worker
// declared one by *echoing* the platform of an assignment it had run - so it
// could not be given a first step until it had run a first step. A real fleet
// therefore delegated nothing: a worker joined, the driver announced it, and
// every step ran on the invoker (E503).
//
// **Two things kept this invisible.** The existing end-to-end test hands the
// scheduler a hand-written worker list rather than the driver's inventory, and
// its nodes state no platform - and placement lets everything through when
// *nothing anywhere* declares one. Both are reasonable in a test and both are
// unlike the build, which reads `Inventory()` and whose nodes resolve to the
// invoker's platform.
//
// So this one takes the worker list from the fleet and states a platform, which
// is what a build does (E504).
func TestAFreshWorkerCanBeGivenWork(t *testing.T) {
	t.Parallel()

	const platform = "linux/arm64"

	session := fleet.Session{Session: "fresh", RunID: "1", Attempt: 1, Repo: "r"}
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

				res, err := remote.Run(ctx, n, core.Worker{ID: "w1"}, a.Base, a.Sources)
				if err != nil {
					return fleet.Reply{}, err
				}

				return fleet.Reply{
					Version: fleet.Version, Layer: res.Layer, Content: res.Content,
					Observation: fleet.Observation{Reads: res.Observation.Reads},
				}, nil
			},
			func(err error) { t.Logf("worker: %v", err) },
			// What it is, said on arrival rather than after it has run.
			fleet.Runs(platform, 4, ""))
	}()

	// Wait for the *declaration*, not merely for the connection: a worker in
	// the inventory with no platform is a worker nothing can be placed on, and
	// waiting only for `Workers() > 0` is what let this pass before.
	var inv []core.Worker

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		inv = r.Inventory()
		if len(inv) > 0 && inv[0].Platform != (ir.Platform{}) {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	if len(inv) == 0 {
		t.Fatal("no worker joined")
	}

	if inv[0].Platform == (ir.Platform{}) {
		t.Fatal("the worker joined and never said what it runs, so placement" +
			" can give it nothing - which is the whole of E503")
	}

	// The build's own arrangement: the inventory is the worker list, and the
	// steps state a platform.
	here := &building{name: "here"}

	workers := append([]core.Worker{
		{ID: "me", IsInvoker: true, Platform: platformOf(t, platform)},
	}, inv...)

	graph := chain(4)
	for _, n := range graph.Nodes() {
		n.Platform = platformOf(t, platform)
	}

	s := &core.Scheduler{
		Workers:  workers,
		Executor: &fleet.Delegating{Local: here, Fleet: r},
		Cache:    &memCache{},
		Blobs:    everyBlob{},
		Writer:   "test",
	}

	if _, err := s.Run(t.Context(), graph); err != nil {
		t.Fatal(err)
	}

	if remote.count() == 0 {
		t.Errorf("every step ran on the invoker and the worker was given none"+
			"\n  invoker ran %d, worker ran 0"+
			"\n  a fleet that is joined and never used is the failure E503"+
			" named", here.count())
	}
}

// platformOf parses a platform as the wire writes one.
func platformOf(t *testing.T, s string) ir.Platform {
	t.Helper()

	os, arch, ok := splitPlatform(s)
	if !ok {
		t.Fatalf("%q is not os/arch", s)
	}

	return ir.Platform{OS: os, Arch: arch}
}

func splitPlatform(s string) (string, string, bool) {
	for i := range len(s) {
		if s[i] == '/' {
			return s[:i], s[i+1:], true
		}
	}

	return "", "", false
}
