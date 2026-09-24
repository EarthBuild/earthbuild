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
)

// A worker joins a driver it was never told the identity of.
//
// C.1's whole point: the driver's endpoint id is **derived from the session and
// the secret**, so a worker that knows the secret knows where to go. There is
// nothing to configure and nothing to leak - and nobody without the secret can
// derive it, join, or serve results into somebody's build.
//
// The worker connects to the driver, not the other way round, which is the
// arrangement that works in the world: a worker is behind whatever NAT its
// operator has, while the driver is the one machine somebody can reach. QUIC is
// bidirectional, so assignments travel back down the connection the worker
// opened, and nothing but the driver has to be reachable.
func TestAWorkerJoinsADriverItDerives(t *testing.T) {
	t.Parallel()

	session := fleet.Session{Session: "s", RunID: "1", Attempt: 1, Repo: "r"}
	secret := []byte("shared")

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	driver, err := fleet.BindDriver(context.Background(), session, secret,
		iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	// The identity the worker derives, and the identity the driver has.
	want, err := fleet.DriverID(session, secret)
	if err != nil {
		t.Fatal(err)
	}

	if driver.ID() != want {
		t.Fatalf("the driver's identity is %v and a worker deriving it gets %v"+
			"\n  nothing would ever connect", driver.ID(), want)
	}

	r := &fleet.Rendezvous{}

	go func() { _ = r.Accept(t.Context(), driver, func(err error) { t.Logf("driver: %v", err) }) }()

	// The worker. It is told an address - C.1 does not say how a worker learns
	// one, and being told is the honest answer - and derives the *identity* from
	// the secret rather than being told that too.
	w, err := iroh.Bind(context.Background(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = w.Shutdown(context.Background()) })

	at := netaddr.NewEndpointAddr(want).WithIP(driver.LocalAddr())

	ran := make(chan fleet.Assignment, 4)

	go func() {
		_ = fleet.Join(t.Context(), w, at,
			func(_ context.Context, a fleet.Assignment) (fleet.Reply, error) {
				ran <- a

				return fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{5}}, nil
			},
			func(err error) { t.Logf("worker: %v", err) })
	}()

	// Wait for the join, then assign. A worker that has not arrived is not a
	// fleet that is broken (E247).
	for deadline := time.Now().Add(10 * time.Second); r.Workers() == 0 &&
		time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}

	if r.Workers() == 0 {
		t.Fatal("no worker joined; a worker that derives the driver's identity" +
			" and is given its address should need nothing else")
	}

	sent := fleet.Assignment{
		Version: fleet.Version,
		Op:      fleet.Op{Kind: fleet.KindExec, Args: []string{"make"}},
	}

	reply, err := r.Assign(t.Context(), sent)
	if err != nil {
		t.Fatalf("the driver could not reach a worker that had joined: %v", err)
	}

	if reply.Layer != (ir.NodeID{5}) {
		t.Errorf("the reply carried %v", reply.Layer)
	}

	select {
	case got := <-ran:
		if !same(sent, got) {
			t.Errorf("what the worker ran is not what was sent:\n  %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Error("the worker replied without receiving anything")
	}
}

// A different session is a different driver.
//
// Prior art on this mechanism records four CI matrix jobs sharing one session
// identifier: the driver's identity is derived from it, so four fleets
// advertised the same driver and the mesh connected them to one another -
// `workers joined: 3/2` on one runner and `0/2` on another.
//
// **A matrix axis belongs in the session term**, and this is the property that
// makes that true rather than a convention.
func TestADifferentSessionIsADifferentDriver(t *testing.T) {
	t.Parallel()

	secret := []byte("shared")

	base := fleet.Session{Session: "s", RunID: "1", Attempt: 1, Repo: "r"}

	first, err := fleet.DriverID(base, secret)
	if err != nil {
		t.Fatal(err)
	}

	for _, other := range []fleet.Session{
		{Session: "t", RunID: "1", Attempt: 1, Repo: "r"},
		{Session: "s", RunID: "2", Attempt: 1, Repo: "r"},
		{Session: "s", RunID: "1", Attempt: 2, Repo: "r"},
		{Session: "s", RunID: "1", Attempt: 1, Repo: "q"},
	} {
		got, driverErr := fleet.DriverID(other, secret)
		if driverErr != nil {
			t.Fatal(driverErr)
		}

		if got == first {
			t.Errorf("%+v derives the same driver as %+v; two fleets would"+
				" advertise one identity and connect to each other", other, base)
		}
	}

	// And without the secret there is no identity at all.
	_, err = fleet.DriverID(base, nil)
	if err == nil {
		t.Error("a driver identity was derived from public metadata alone;" +
			" anyone watching the repository could join")
	}
}

// A worker not on the allowlist is not admitted.
//
// C.1: deriving the driver's identity is necessary and **not sufficient**. A
// secret can leak; an allowlist can be narrowed without rotating one.
//
// Checked at accept, against the identity **QUIC verified during the
// handshake** rather than one this engine was told - so a peer cannot claim to
// be somebody on the list. That is a better place for the check than the one it
// was in, where the driver decided before dialling and the arrangement had the
// driver dialling at all (E250).
func TestAWorkerOffTheAllowlistIsNotAdmitted(t *testing.T) {
	t.Parallel()

	session := fleet.Session{Session: "gate", RunID: "1", Attempt: 1, Repo: "r"}
	secret := []byte("shared")

	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	driver, err := fleet.BindDriver(context.Background(), session, secret,
		iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

	// An allowlist naming somebody else entirely.
	r := &fleet.Rendezvous{Allow: fleet.NewAllowlist(make([]byte, 32))}

	refused := make(chan struct{}, 1)

	go func() {
		_ = r.Accept(t.Context(), driver, func(error) {
			select {
			case refused <- struct{}{}:
			default:
			}
		})
	}()

	w, err := iroh.Bind(context.Background(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = w.Shutdown(context.Background()) })

	id, err := fleet.DriverID(session, secret)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		_ = fleet.Join(t.Context(), w,
			netaddr.NewEndpointAddr(id).WithIP(driver.LocalAddr()),
			func(context.Context, fleet.Assignment) (fleet.Reply, error) {
				return fleet.Reply{Version: fleet.Version}, nil
			}, nil)
	}()

	select {
	case <-refused:
	case <-time.After(10 * time.Second):
		t.Fatal("the driver neither admitted nor refused the worker")
	}

	if r.Workers() != 0 {
		t.Errorf("%d workers joined; deriving the driver's identity got this"+
			" one to the door and must not have got it through", r.Workers())
	}
}
