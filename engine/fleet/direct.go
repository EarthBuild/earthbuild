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
// **Zero, because the transfer was never the cost.** Splitting the two numbers
// settled it in one run:
//
//	fetched from fb05f586… over ip:57.151.129.40:37969
//	  (reached in 3363ms, read in 302ms)
//
// 7.9 MiB in 302ms is 26 MiB/s, which is what that network should do. The whole
// of a fleet's apparent transfer cost on GitHub is *reaching the peer* -
// discovery, handshake, hole punching - and three seconds of the 3363 above is
// this wait, buying a route that saves nothing measurable.
//
// The re-dial is gated on the same setting and is off with it. Both are kept
// because the route is genuinely better and will matter on a base where 302ms
// becomes minutes; neither is worth a fixed three seconds today.
const defaultDirectWait = 0

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
