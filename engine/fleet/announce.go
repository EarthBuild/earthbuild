package fleet

import (
	"net"
	"net/netip"
	"strings"
)

// correctHost replaces an unroutable announced host with the one observed.
//
// A worker bound to everything reports `[::]:50277`, which names *this machine's
// sockets* rather than a place: a peer handed it dials its own loopback. The
// worker cannot do better - a machine with several interfaces has no way to know
// which the driver can see, and behind NAT the answer is none of them - and the
// driver can, because it observed the connection (E277).
//
// **Only the host.** The observed port belongs to the control connection, an
// ephemeral one on a different socket from the one serving blobs, so taking the
// whole address would point every peer at a port that answers nothing.
//
// That is also where this stops being general: a NAT that remaps ports breaks
// it, which is what endpoint discovery and relays exist for. On a LAN, and on
// any network that translates addresses without renumbering ports, it is right.
//
// An announcement that is already a real address is believed. A worker may be
// reachable somewhere the driver's view does not mention - the driver saw one
// path to it, not the only one.
func correctHost(announced string, seen net.Addr) string {
	if seen == nil {
		return announced
	}

	id, host, ok := strings.Cut(announced, "@")
	if !ok || id == "" || host == "" {
		// Not something this understands. Passed through rather than mangled:
		// it will fail to dial and be skipped, which is what a wrongly corrected
		// address would do anyway, without turning a diagnosable failure into a
		// confusing one.
		return announced
	}

	ap, err := netip.ParseAddrPort(host)
	if err != nil || !ap.Addr().IsUnspecified() {
		return announced
	}

	from, err := netip.ParseAddrPort(seen.String())
	if err != nil {
		return announced
	}

	return id + "@" + netip.AddrPortFrom(from.Addr().Unmap(), ap.Port()).String()
}
