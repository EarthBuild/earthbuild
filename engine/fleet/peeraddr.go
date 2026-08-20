package fleet

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
)

// PeerAddr is where a worker can be reached for the blobs it holds.
//
// Two identities, and both are needed: **who** is verified by the QUIC handshake
// and **where** is a hint about how to get there. A string carrying only the
// host would connect to whatever answers on that port; one carrying only the
// identity needs discovery this engine does not run.
//
// Written as `<identity-hex>@<host:port>` because it travels as one field of an
// advisory hint (E260), through a driver that does not interpret it.
type PeerAddr struct {
	ID   key.EndpointID
	Host string
}

// String is the form that crosses the wire.
//
// The identity in the library's own text form rather than one invented here: a
// second spelling of an identity is a second thing to get out of step with the
// first, and this one is what every diagnostic already prints.
func (p PeerAddr) String() string {
	return p.ID.String() + "@" + p.Host
}

// ParsePeerAddr reads one, refusing anything it cannot fully understand.
//
// Fails closed. The string came from another machine's claim about itself (A5),
// and a parser that accepted a truncated identity would dial *something* - with
// whatever the handshake then let through.
func ParsePeerAddr(s string) (PeerAddr, error) {
	id, host, ok := strings.Cut(s, "@")
	if !ok || id == "" || host == "" {
		return PeerAddr{}, fmt.Errorf("peer address %q is not <identity>@<host:port>", s)
	}

	var out PeerAddr

	err := out.ID.UnmarshalText([]byte(id))
	if err != nil {
		return PeerAddr{}, fmt.Errorf("peer address %q: %w", s, err)
	}

	if out.ID.IsZero() {
		return PeerAddr{}, fmt.Errorf("peer address %q names no machine", s)
	}

	out.Host = host

	return out, nil
}

// Endpoint is this address as something to dial.
func (p PeerAddr) Endpoint() (netaddr.EndpointAddr, error) {
	at := netaddr.NewEndpointAddr(p.ID)

	ap, err := parseAddrPort(p.Host)
	if err != nil {
		return at, err
	}

	return at.WithIP(ap), nil
}

// parseAddrPort is netip's, wrapped so the error names what was wrong.
func parseAddrPort(s string) (netip.AddrPort, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("peer host %q is not ip:port: %w", s, err)
	}

	return ap, nil
}
