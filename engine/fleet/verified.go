package fleet

import (
	"context"
	"errors"
	"fmt"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
)

// errNoAddressVouched is what is left when every address a peer was offered at
// failed its handshake.
var errNoAddressVouched = errors.New("no address the peer was offered at completed a handshake as that peer")

// dialVerified connects to peer and returns the connection only once its
// handshake has vouched for the peer's identity.
//
// **A 0-RTT connection is an assertion, not an identity.** With a session
// ticket cached, `Connect` returns before the handshake - go-iroh's
// `Connection` does not wait for it on the early path - on the first address
// that took the packets, and `Paths` reports that path as validated. An address
// that names a different machine wherever it is dialled takes them: the ULA
// `fd15:70a:510b:1::2` is on every GitHub runner. The handshake then fails, as
// `server identity mismatch` when the stranger answers and as an idle timeout
// when it does not, after the caller has committed to the connection, and the
// peer's real addresses are never tried. fleet-e2e lost about one run in nine
// to it.
//
// So nothing is held until the handshake completes, and an address whose
// handshake failed is struck from the peer and the rest are dialled. The cost
// is a round trip on the first fetch from a peer that would otherwise have been
// 0-RTT, paid once per connection.
func dialVerified(ctx context.Context, e *iroh.Endpoint, peer netaddr.EndpointAddr, alpn string) (*iroh.Conn, error) {
	for {
		c, err := e.Connect(ctx, peer, alpn)
		if err != nil {
			return nil, err //nolint:wrapcheck // callers say what they were connecting for
		}

		// **Taken now, not after the failure.** A connection that has closed
		// reports no paths, so asked afterwards the address that failed has no
		// name and cannot be struck - one connect in eight, measured.
		at, named := pathAddr(c.Paths())

		select {
		case <-c.HandshakeComplete():
			return c, nil
		case <-ctx.Done():
			_ = c.Close()

			return nil, fmt.Errorf("waiting for %s to complete a handshake: %w", peer.ID, ctx.Err())
		case <-c.Context().Done():
		}

		// The handshake failed, so the address this connection went to is not
		// this peer. Struck, and the rest tried; an address that cannot be
		// named is not struck, which ends the loop rather than repeating it.
		if !named {
			return nil, fmt.Errorf("connect to %s: %w", peer.ID, errNoAddressVouched)
		}

		rest, struck := without(peer, at)
		if !struck {
			return nil, fmt.Errorf("connect to %s: %w (%s failed it, and is not among the addresses offered)",
				peer.ID, errNoAddressVouched, at)
		}

		peer = rest
		if peer.IsEmpty() {
			return nil, fmt.Errorf("connect to %s: %w (the last, %s, answered as another machine or not at all)",
				peer.ID, errNoAddressVouched, at)
		}
	}
}

// pathAddr is the transport address a connection went to: its first path's.
func pathAddr(paths []iroh.PathInfo) (netaddr.TransportAddr, bool) {
	for _, p := range paths {
		if p.HasAddr {
			return p.Addr, true
		}
	}

	return nil, false
}

// without is peer with one transport address removed, and whether it was there.
//
// By Compare rather than ==: a path's address and the one it was dialled from
// are equal values, not necessarily one value, and a strike that matched
// nothing would dial the same addresses again for ever.
func without(peer netaddr.EndpointAddr, gone netaddr.TransportAddr) (netaddr.EndpointAddr, bool) {
	kept := make([]netaddr.TransportAddr, 0, len(peer.Addrs()))

	for _, a := range peer.Addrs() {
		if a.Compare(gone) != 0 {
			kept = append(kept, a)
		}
	}

	return netaddr.NewEndpointAddr(peer.ID, kept...), len(kept) < len(peer.Addrs())
}
