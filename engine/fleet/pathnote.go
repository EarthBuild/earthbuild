package fleet

import (
	"fmt"
	"strings"
	"time"

	"github.com/tmc/go-iroh/iroh"
)

// pathNote describes the route a connection's bytes are taking.
//
// **A relay is a detour through the public internet and nothing said when one
// was taken.** Relays are configured on purpose - two NAT'd CI runners may have
// no other way to reach each other (E505) - but a build that got one is paying
// a round trip to another continent for every window, and a build that
// hole-punched is not. Two GitHub runners in the same datacentre moved 7.9 MiB
// in 6.366s, and the log could not say which of those had happened.
//
// Validated paths only. A path being probed is one that might carry bytes
// later; naming it would describe a route the transfer never used.
//
// Empty when there is nothing validated to report, so a caller can print it or
// not without asking twice.
func pathNote(paths []iroh.PathInfo) string {
	var out []string

	for _, p := range paths {
		if !p.Validated || !p.HasAddr || p.Addr == nil {
			continue
		}

		// `Network` is the transport kind - "relay", "ip" or "custom" - and
		// `String` renders it as "kind:value", so the kind is already in the
		// text. What is added here is the round trip, which is what makes a
		// relay legible as a cost rather than as a spelling.
		at := p.Addr.String()

		if p.HasRTT {
			at += fmt.Sprintf(" rtt %v", p.RTT.Round(100*time.Microsecond))
		}

		// **Both directions, because a fetcher is a receiver.** Reading
		// `BytesSent` alone on the machine doing the fetching reports the size
		// of its *request* and calls a path idle when it is carrying the whole
		// transfer the other way - which is how `sent 0 B` on a direct path was
		// read here as "the bytes are going via the relay".
		if p.HasBytesSent {
			at += " sent " + human(p.BytesSent)
		}

		if p.HasBytesReceived {
			at += " received " + human(p.BytesReceived)
		}

		out = append(out, at)
	}

	return strings.Join(out, ", ")
}

// human is a byte count somebody can read.
func human(n uint64) string {
	const unit = 1024

	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := uint64(unit), 0
	for n/div >= unit && exp < 4 {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.3g %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
