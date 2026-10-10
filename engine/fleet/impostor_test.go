package fleet_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// An address that answers as another machine does not stop the fetch.
//
// fleet-e2e failed about one run in nine with `connect for blobs: … server
// identity mismatch: dialed <A>, got <B>`, dialling `[fd15:70a:510b:1::2]` every
// time: a ULA that every GitHub runner carries, so a worker advertising it
// names whichever runner dials it. iroh is right to refuse the stranger. What
// was wrong is that the refusal ended the connect while the peer's real
// addresses went untried.
//
// It only ends there on a *re*connect. With a session ticket cached, the dial
// returns before the handshake (0-RTT), the identity check fails afterwards,
// and by then the loop over the peer's addresses has committed to the first
// one. So this caches a ticket first, then offers the impostor ahead of the
// real address - IPv4 sorts before IPv6, which makes the order certain.
func TestAnAddressThatAnswersAsAnotherMachineIsPassedOver(t *testing.T) {
	t.Parallel()

	client, server := endpointsFor(t, fleet.ALPNBlob)

	store, _, ids := storeWith(t, "one", "two")

	go func() {
		_ = fleet.ServeBlobs(t.Context(), server, store,
			func(err error) { t.Logf("server: %v", err) })
	}()

	impostor, err := iroh.Bind(context.Background(), iroh.WithALPNs(fleet.ALPNBlob),
		iroh.WithBindAddr(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)))
	if err != nil {
		t.Skipf("no IPv4 loopback endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = impostor.Shutdown(context.Background()) })

	// A first fetch, by the real address, so the client holds a ticket for the
	// server's identity and the next connect takes the early path.
	first := &fleet.Fetch{Peers: []fleet.Source{&fleet.PeerSource{
		Label: "peer", Endpoint: client, Peer: loopback(t, server),
	}}}
	retryFetch(t, first, ids)

	// The server's identity, reachable at its own address - and, first in the
	// dial order, at an address where a different machine answers.
	confused := netaddr.NewEndpointAddr(server.ID()).
		WithIP(impostor.LocalAddr()).
		WithIP(server.LocalAddr())

	again := &fleet.PeerSource{Label: "peer", Endpoint: client, Peer: confused}

	if _, err := (&fleet.Fetch{Peers: []fleet.Source{again}}).Get(t.Context(), ids); err != nil {
		t.Errorf("one wrong address among right ones failed the fetch: %v"+
			"\n  a stranger answering at one address says nothing about the others,"+
			" and the peer was reachable at %s", err, server.LocalAddr())
	}
}
