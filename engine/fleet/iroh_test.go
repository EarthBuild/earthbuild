package fleet_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
)

// endpointsFor binds a pair, the second listening on this protocol.
func endpointsFor(t *testing.T, alpn string) (client, server *iroh.Endpoint) {
	t.Helper()

	// Bound to loopback explicitly. Left to itself an endpoint binds the
	// wildcard, `LocalAddr` then reports an unspecified address, and an address
	// built from it names a socket nobody is listening on - which presents as a
	// connection that establishes and then refuses the stream, with no
	// explanation at either end (E247).
	local := netip.AddrPortFrom(netip.IPv6Loopback(), 0)

	client, err := iroh.Bind(context.Background(), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = client.Shutdown(context.Background()) })

	server, err = iroh.Bind(context.Background(),
		iroh.WithALPNs(alpn), iroh.WithBindAddr(local))
	if err != nil {
		t.Skipf("no second endpoint here: %v", err)
	}

	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	return client, server
}

// loopback is a worker's address as another endpoint in this process can reach it.
//
// Built rather than discovered. `Endpoint.Addr()` is populated by discovery -
// a relay tells an endpoint how the world sees it - and there is no relay here
// and should not be: two endpoints in one process are testing the **wire**, not
// the internet, and a test that needed a relay would be a test of somebody
// else's uptime.
//
// `LocalAddr` binds to all interfaces, so the address it reports has no usable
// IP; the port is the part that matters and loopback is where the other endpoint
// is.
func loopback(t *testing.T, e *iroh.Endpoint) netaddr.EndpointAddr {
	t.Helper()

	if e.LocalAddr().Port() == 0 {
		t.Skip("the endpoint bound no port, so nothing can reach it")
	}

	// From the identity and the socket, which is what the endpoint is actually
	// listening on - rather than from `Addr()`, whose transport addresses come
	// from discovery and are empty without a relay.
	return netaddr.NewEndpointAddr(e.ID()).WithIP(e.LocalAddr())
}
