package fleet

import (
	"context"
	"os"
	"time"

	"github.com/tmc/go-iroh/iroh"
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
// **Zero, because waiting was measured and did not help.** Three runs at
// 6.213s, 8.226s and 9.095s for the same 7.9 MiB - noise around no
// improvement, and the two slower ones are the ones that waited.
//
// The reading that prompted this was wrong and is worth recording: the direct
// path reported `sent 0 B`, which was taken as "the data is going via the
// relay". A fetcher is a *receiver*, so its send counter is the size of its
// request whatever path carries the reply, and `pathNote` now reports both
// directions. What survives is the timing, which says the wait buys nothing on
// GitHub.
//
// The mechanism is kept: if the route ever turns out to be the cost, this is
// the only line that changes.
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
