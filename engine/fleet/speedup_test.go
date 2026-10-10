package fleet_test

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A fleet is faster than one machine when the work is worth moving.
//
// **Nothing in this project had measured that.** Every experiment so far counted
// bytes, which is the cost side; this is the first that puts seconds on the
// other side of the ledger, and it is the claim the whole effort rests on - a
// distributed build that is correct and no faster is the outcome of the two
// previous attempts.
//
// The step's compute is synthetic and the transfer is over loopback, so the
// figure is not a benchmark. What it establishes is the *shape*: with base
// affinity, single-flight provisioning and peer-to-peer transfer, more machines
// finish sooner. A regression that reintroduced any of those would show up here
// as a fleet that is no faster, which is the symptom nothing else in the suite
// can produce.
func TestAFleetIsFasterThanOneMachine(t *testing.T) {
	t.Parallel()

	const (
		wide    = 6
		compute = 250 * time.Millisecond
	)

	// The faster of two, for each. A timing test on a shared machine measures
	// the machine as much as the code, and the *slow* direction is the noisy one
	// - a scheduler hiccup can only add. Taking the best of two removes most of
	// that without inventing a result: neither run is allowed to be faster than
	// the work actually takes.
	one := min(timeFanOut(t, 1, wide, compute), timeFanOut(t, 1, wide, compute))
	three := min(timeFanOut(t, 3, wide, compute), timeFanOut(t, 3, wide, compute))

	t.Logf("%d steps of %v: one machine %v, three machines %v (%.2f×)",
		wide, compute, one, three, float64(one)/float64(three))

	if three >= one {
		t.Fatalf("three machines took %v and one took %v"+
			"\n  this is the outcome the two previous attempts at a distributed"+
			" build reached, and the one this design exists to avoid", three, one)
	}

	// Generously: the ideal is 2 waves against 6, and anything under a third
	// saved on a loaded machine means something has stopped overlapping.
	if float64(three) > 0.7*float64(one) {
		t.Errorf("three machines saved only %.0f%% of one machine's time"+
			"\n  six steps over three workers should be about two waves, not"+
			" six", 100*(1-float64(three)/float64(one)))
	}
}

// timeFanOut runs one step and then a fan-out from it, on n workers, and says
// how long the fan-out took.
func timeFanOut(t *testing.T, n, wide int, compute time.Duration) time.Duration {
	t.Helper()

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)
	session := fleet.Session{Session: "speed", RunID: "1", Attempt: n, Repo: "r"}
	secret := []byte("shared")

	driver, err := fleet.BindDriver(t.Context(), session, secret, iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = driver.Shutdown(context.WithoutCancel(t.Context())) })

	r := &fleet.Rendezvous{Reach: 30 * time.Second}

	go func() { _ = r.Accept(t.Context(), driver, func(error) {}) }()

	id, err := fleet.DriverID(session, secret)
	if err != nil {
		t.Fatal(err)
	}

	at := netaddr.NewEndpointAddr(id).WithIP(driver.LocalAddr())

	for i := range n {
		// One step at a time, so a machine in this measurement is a machine:
		// with unlimited capacity one process is infinitely parallel and the
		// comparison has nothing to say (E271).
		startWorker(t, i, local, at, 1, compute)
	}

	for deadline := time.Now().Add(30 * time.Second); r.Workers() < n &&
		time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}

	if r.Workers() < n {
		t.Skipf("only %d of %d worker(s) joined", r.Workers(), n)
	}

	d := &fleet.Delegating{Local: &countingLocal{}, Fleet: r}

	first, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	began := time.Now()

	var wg sync.WaitGroup

	for range wide {
		wg.Go(func() {
			_, _ = d.Run(t.Context(), delegable(), core.Worker{ID: "w"},
				[]ir.NodeID{first.Layer}, nil)
		})
	}

	wg.Wait()

	return time.Since(began)
}
