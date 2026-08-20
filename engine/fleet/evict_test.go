package fleet_test

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func someAssignment() fleet.Assignment {
	return fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
	}
}

// A worker that could not be reached stops being offered steps.
//
// `Assign` already tries each worker once, so one dead machine does not fail one
// step. Left in the list, though, it fails *every* step: each assignment pays a
// dial to a machine that is gone before reaching one that is not, and the wait
// is the connection timeout rather than nothing. C.5 calls this reassignment,
// and reassignment that never removes anything is just a retry with a growing
// bill.
func TestAWorkerThatCouldNotBeReachedIsDropped(t *testing.T) {
	t.Parallel()

	r := &fleet.Rendezvous{}
	r.AddForTest()
	r.AddForTest()

	if r.Workers() != 2 {
		t.Fatalf("started with %d worker(s), want 2", r.Workers())
	}

	_, err := r.Assign(t.Context(), someAssignment())
	if !errors.Is(err, fleet.ErrWorkerGone) {
		t.Fatalf("assigning to two unreachable workers gave %v,"+
			" want ErrWorkerGone", err)
	}

	if r.Workers() != 0 {
		t.Errorf("%d worker(s) still in the fleet after every one of them"+
			" failed\n  the next step pays a dial to each of them again",
			r.Workers())
	}
}

// A worker that joins after another has left does not inherit its name.
//
// The inventory names workers so the scheduler can place steps and the cache can
// attribute what they produce. If those names came from a position in a slice,
// eviction would shift them: `fleet-1` would mean one machine before a departure
// and a different machine after, and a step's cache entry would be attributed to
// whichever machine happened to be standing there. §4.7.3 wants a schedule that
// can be reproduced from the inventory, and a name that silently changes hands
// makes that impossible to check.
func TestANewWorkerDoesNotInheritADepartedName(t *testing.T) {
	t.Parallel()

	r := &fleet.Rendezvous{}
	r.AddForTest()
	r.AddForTest()

	before := r.Inventory()
	if len(before) != 2 {
		t.Fatalf("inventory of %d, want 2", len(before))
	}

	// Both fail, so both leave.
	_, _ = r.Assign(t.Context(), someAssignment())

	r.AddForTest()

	after := r.Inventory()
	if len(after) != 1 {
		t.Fatalf("inventory of %d after two left and one joined, want 1", len(after))
	}

	for _, old := range before {
		if after[0].ID == old.ID {
			t.Errorf("the worker that joined is called %q, which is what a"+
				" worker that has left was called"+
				"\n  a step's cache entry would be attributed to a machine that"+
				" never ran it", after[0].ID)
		}
	}
}

// A worker whose machine goes away is dropped from the real fleet.
//
// The unit tests above drive eviction through a connection that was never alive.
// This is the case that actually happens - a CI runner reclaimed, a laptop
// closed - and it is worth its seconds because the failure a live connection
// produces when its far end vanishes is not the failure a nil one produces, and
// only one of them is on the path this engine will meet.
func TestAWorkerWhoseMachineWentAwayIsDropped(t *testing.T) {
	t.Parallel()

	session := fleet.Session{Session: "evict", RunID: "1", Attempt: 1, Repo: "r"}
	secret := []byte("shared")
	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	driver, err := fleet.BindDriver(t.Context(), session, secret,
		iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

	// Short, because the point being measured is that a dead worker costs a
	// bounded wait rather than QUIC's idle timeout - which is 30 seconds, and
	// is 30 seconds *per step* for as long as the corpse stays in the fleet.
	r := &fleet.Rendezvous{Reach: 2 * time.Second}

	go func() { _ = r.Accept(t.Context(), driver, func(err error) { t.Logf("driver: %v", err) }) }()

	w, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	id, err := fleet.DriverID(session, secret)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		_ = fleet.Join(t.Context(), w,
			netaddr.NewEndpointAddr(id).WithIP(driver.LocalAddr()),
			func(_ context.Context, _ fleet.Assignment) (fleet.Reply, error) {
				return fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{7}}, nil
			},
			func(err error) { t.Logf("worker: %v", err) })
	}()

	for deadline := time.Now().Add(10 * time.Second); r.Workers() == 0 &&
		time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}

	if r.Workers() == 0 {
		t.Skip("no worker joined; nothing to lose")
	}

	// The machine goes away. Not a graceful leave - there is no such message in
	// C.5, and a reclaimed runner does not send one.
	_ = w.Shutdown(t.Context())

	start := time.Now()

	_, err = r.Assign(t.Context(), someAssignment())
	if err == nil {
		t.Fatal("a worker that had shut down answered an assignment")
	}

	// Bounded by Reach, not by the transport's own patience. Generously, so a
	// loaded machine does not fail this - the failure being guarded against is
	// tens of seconds, not hundreds of milliseconds.
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("reaching a machine that is gone took %v"+
			"\n  a driver waits Reach (%v) for a worker, or every step of a"+
			" build pays the transport's idle timeout", took, r.Reach)
	}

	if r.Workers() != 0 {
		t.Errorf("%d worker(s) left after the only machine went away"+
			"\n  every later step dials it before reaching anything real",
			r.Workers())
	}
}

// A blob fetch honours the deadline it was given.
//
// The same gap as the control protocol had, in the other protocol: the context
// covers connecting and opening a stream, and the reads that follow take no
// context at all. A peer that went away after the stream opened would hold the
// fetch until QUIC gave up on the connection - and because a fetch tries its
// sources in order (I6, E237), a peer that hangs blocks the *next* source too.
// Multi-source fallback that waits half a minute per corpse is not fallback.
func TestABlobFetchGivesUpWhenItsDeadlinePasses(t *testing.T) {
	t.Parallel()

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	holder, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local),
		iroh.WithALPNs(fleet.ALPNBlob))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	asker, err := iroh.Bind(t.Context(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = asker.Shutdown(t.Context()) })

	// Accepts the connection and then says nothing at all - the shape a wedged
	// or half-dead peer takes, which is worse than one that has closed because
	// nothing arrives to notice.
	go func() {
		conn, err := holder.Accept(t.Context())
		if err != nil {
			return
		}

		_, _ = conn.AcceptStream(t.Context())

		<-t.Context().Done()
	}()

	src := &fleet.PeerSource{
		Endpoint: asker,
		Peer:     netaddr.NewEndpointAddr(holder.ID()).WithIP(holder.LocalAddr()),
		Label:    "wedged",
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	// Off the test's own goroutine, watched. Asserting the elapsed time after
	// the call returns can only *fail slowly* - and the failure here is a fetch
	// that never returns at all, which would hang the suite rather than report
	// anything. A test whose failure mode is a hang is a test that will one day
	// be blamed on the machine.
	type answer struct {
		got map[ir.NodeID]io.Reader
		err error
	}

	done := make(chan answer, 1)

	go func() {
		got, err := src.Fetch(ctx, []ir.NodeID{{1}})
		done <- answer{got, err}
	}()

	select {
	case a := <-done:
		if a.err != nil {
			t.Logf("fetch: %v", a.err)
		}

		// Nothing, and no error, is how this source says "ask somebody else"
		// (I6): the deadline passing must produce an empty result rather than a
		// failure, or a peer being silent would fail a build that another
		// holder could have satisfied.
		if len(a.got) != 0 {
			t.Errorf("a peer that never answered produced %d blob(s)", len(a.got))
		}

	case <-time.After(15 * time.Second):
		t.Fatal("a fetch from a silent peer never returned" +
			"\n  the 2s deadline bounds the connect and not the reads, so every" +
			" later source waits behind this one indefinitely")
	}
}
