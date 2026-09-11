package guest

import (
	"errors"
	"net"
	"net/netip"
)

// Uplink is the guest's own interface, and the segment a step's macvlan joins.
//
// **The segment travels with the name.** `uplink` used to return the name alone
// after reading the addresses to find it, and the caller then addressed the step
// from a subnet written down in this package - which was this engine's own
// microVM's, and was wrong on every other backend. See vmStepNetOn.
type Uplink struct {
	// Name of the interface, found rather than named: a guest's NIC is called
	// `eth0` until a kernel decides otherwise.
	Name string
	// Subnet the interface is on, which is the segment a step joins.
	Subnet netip.Prefix
	// Addr is the guest's own address on it, which no step may be given.
	Addr netip.Addr
}

// errNoUplink says no interface on this guest could carry a step.
var errNoUplink = errors.New("this guest has no interface a step could share")

// uplinkAmong picks the interface a step's network hangs off.
//
// Pure over what `net.Interfaces` reports, so the choice is testable without a
// kernel - which matters because the failure it guards against is silent: a
// guest that picks the wrong interface builds steps onto a segment with no
// route off it, and every one of them reports a connection failure rather than
// a configuration one.
//
// The first interface that is up, is not loopback, and has an IPv4 address with
// a prefix. IPv6 is skipped rather than handled: a macvlan on an IPv6-only
// segment needs an allocator this does not have, and picking such an interface
// would produce a step that cannot be addressed at all.
func uplinkAmong(ifaces []net.Interface, addrsOf func(net.Interface) ([]net.Addr, error)) (Uplink, error) {
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := addrsOf(i)
		if err != nil {
			continue
		}

		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}

			at, ok := netip.AddrFromSlice(ipnet.IP.To4())
			if !ok {
				continue
			}

			ones, bits := ipnet.Mask.Size()
			if bits != 32 {
				continue
			}

			return Uplink{
				Name:   i.Name,
				Subnet: netip.PrefixFrom(at, ones).Masked(),
				Addr:   at,
			}, nil
		}
	}

	return Uplink{}, errNoUplink
}
