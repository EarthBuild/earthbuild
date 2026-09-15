package fleet

import (
	"context"
	"net/netip"
	"os"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
)

// EnvDirectWait bounds how long a blob connection waits for a hole-punched path
// before transferring over a relay.
//
// **A relay is a detour and the transfer does not have to take it.** Two GitHub
// runners in the same datacentre fetched through a relay in us-west-1 and moved
// 7.9 MiB at about 1.2 MiB/s; the connection was up in milliseconds and the
// direct path arrives shortly afterwards, but the fetch had already started on
// whatever was validated first.
//
// Bounded, and short: where hole punching cannot land - which is the case
// relays exist for (E505) - this is pure delay, once per peer, and the build
// proceeds on the relay having spent it. Set to `0` to transfer on whatever is
// available.
const EnvDirectWait = "EARTH_FLEET_DIRECT_WAIT"

// defaultDirectWait is what a connection gives hole punching.
//
// **Three seconds, and it now buys something.** Waiting alone did not: three
// runs at 6.213s, 8.226s and 9.095s against 6.124s without, which is noise
// around no improvement, because a validated direct path sat idle while the
// relay carried everything.
//
// What the wait is for is `redialDirect`, which needs an observed address to
// dial and can only get one from a connection that has already punched. The
// wait produces the address; the second connection is what actually moves the
// bytes off the relay.
//
// Bounded and short: where hole punching cannot land - which is what relays
// exist for (E505) - this is three seconds per peer per build and the fetch
// proceeds on the relay having spent it. Zero disables both the wait and the
// re-dial, which is the behaviour every build had before.
const defaultDirectWait = 3 * time.Second

// directIn reports whether any validated path is a direct one.
//
// Validated, because a probing path is one that might carry bytes later. A wait
// that stopped on one would hand the transfer to the relay anyway, having
// waited.
func directIn(paths []iroh.PathInfo) bool {
	for _, p := range paths {
		if p.Validated && p.HasAddr && p.Addr != nil && p.Addr.Network() == "ip" {
			return true
		}
	}

	return false
}

// holdForDirect waits briefly for hole punching to land.
//
// Returns as soon as a direct path is validated, when the bound expires, or
// when the connection cannot report paths at all - in which case there is
// nothing to wait for and the caller proceeds on what it has.
func holdForDirect(ctx context.Context, c *iroh.Conn, within time.Duration) {
	if within <= 0 || directIn(c.Paths()) {
		return
	}

	bounded, cancel := context.WithTimeout(ctx, within)
	defer cancel()

	paths, err := c.WatchPaths(bounded)
	if err != nil {
		// Path observation is unavailable on this connection. Not an error:
		// the transfer works on the path it has.
		return
	}

	for seen := range paths {
		if directIn(seen) {
			return
		}
	}
}

// directWait reads the bound.
func directWait() time.Duration {
	v := os.Getenv(EnvDirectWait)
	if v == "" {
		return defaultDirectWait
	}

	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		// A misspelling takes the default rather than being read as "do not
		// wait": the setting is an optimisation, and a typo that silently
		// switched it off is a build that got slower for no visible reason.
		return defaultDirectWait
	}

	return d
}

// directAddr is a validated direct path's address, when there is one.
//
// **The only place a peer's routable address appears.** A worker announces a
// wildcard - `<id>@[::]:40682` - so nothing can be dialled from what the fleet
// carries; the endpoints observe each other during the handshake, and the
// result is here. A second connection made with this and nothing else has no
// relay to fall back to.
//
// Validated only, for the reason `directIn` gives: a probing path may never come
// up, and replacing a working relay connection with one that does not is worse
// than the detour.
func directAddr(paths []iroh.PathInfo) (netip.AddrPort, bool) {
	for _, p := range paths {
		if !p.Validated || !p.HasAddr {
			continue
		}

		if ip, ok := p.Addr.(netaddr.IPAddr); ok {
			return ip.Addr, true
		}
	}

	return netip.AddrPort{}, false
}
