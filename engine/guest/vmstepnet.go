package guest

import (
	"fmt"
	"net/netip"
)

// VMStepNet is one step's place on the guest's own switch.
//
// Flat rather than a /30 per step, which is what the veth arrangement needs:
// here every step is a port on one switch, so they share a subnet and a
// gateway and differ only by address. That is also why no NAT is involved -
// the switch forwards, and outbound traffic leaves by the guest dialling from
// its own network.
type VMStepNet struct {
	// Link is the interface inside the step's namespace.
	Link string
	// Addr is the step's own address, and Gateway the switch's.
	Addr    netip.Addr
	Gateway netip.Addr
	// Subnet is what both sit in.
	Subnet netip.Prefix
	// MAC is the interface's hardware address, derived so two steps cannot
	// collide on one. Unused for an ipvlan child, which shares its parent's.
	MAC string
	// Kind is the sort of link to make: see LinkMACVLAN and LinkIPVLAN.
	Kind string
}

// The two ways a step can be put on its parent's segment.
//
// **A macvlan gives the child its own MAC**, which is the better arrangement
// where anything will carry it: the child is simply another host, and the
// segment's switch learns it like any other.
//
// **An ipvlan child shares its parent's MAC** and is told apart by address.
// That is what gets past a virtual NIC which forwards one MAC and drops the
// rest - Apple's Virtualization.framework does exactly that, so a macvlan step
// there cannot reach its own gateway, which is not a routing failure but a
// layer-2 one and reads as neither.
const (
	LinkMACVLAN = "macvlan"
	LinkIPVLAN  = "ipvlan"
)

// vmStepNetOn derives a step's network from its number and the segment its
// parent NIC is on.
//
// **The segment is the parent's, not a constant.** A macvlan makes a step
// another host on the segment the guest's NIC is already on, so its address has
// to come from that NIC. Writing the subnet down here instead worked on this
// engine's own microVM, whose switch is 192.168.127.0/24, and addressed a step
// onto a segment that does not exist on any backend whose VM sits elsewhere:
// measured on Apple's `container`, whose VM is on 192.168.64.0/24, a step came
// up on 192.168.127.87 with a default route via 192.168.127.1 and could not
// reach a literal address, let alone resolve a name.
//
// Pure, so the arithmetic is testable without a kernel: the failures that
// matter here are two steps given one address, a step given the gateway's or
// the guest's own, an address outside the segment, and a name too long for
// IFNAMSIZ - none of which needs a namespace to demonstrate.
//
// Wrapping rather than failing when the segment is small. A build with more
// concurrent steps than the segment holds would reuse an address while the
// first holder still had it, which is worth knowing rather than worth guarding:
// the guest's own concurrency is bounded far below a /24, and a guard would be
// untested code standing in front of an impossibility.
func vmStepNetOn(i int, subnet netip.Prefix, own netip.Addr) VMStepNet {
	return vmStepNetKind(i, subnet, own, LinkMACVLAN)
}

// vmStepNetKind is vmStepNetOn told which sort of link to make.
func vmStepNetKind(i int, subnet netip.Prefix, own netip.Addr, kind string) VMStepNet {
	base := subnet.Masked().Addr().As4()
	gateway := netip.AddrFrom4([4]byte{base[0], base[1], base[2], 1})

	// The hosts this segment's last octet can hold, less the two that are
	// already real: the gateway, and whatever the guest itself answers to. A
	// step given either collides with a live host and the switch resolves that
	// by dropping one of them, silently.
	//
	// Enumerated and then indexed, rather than shifted past on collision.
	// Shifting reads as obviously correct and is not: step `i` moving to `i+1`'s
	// address collides with step `i+1`, which is what the first version of this
	// did and what its own test caught.
	//
	// A segment wider than a /24 is not walked further. The concurrency that
	// would need it does not exist, and arithmetic nobody can check is worse
	// than a bound somebody can read.
	free := make([]byte, 0, 254)

	for h := 1; h <= 254; h++ {
		at := netip.AddrFrom4([4]byte{base[0], base[1], base[2], byte(h)})
		if at != gateway && at != own {
			free = append(free, byte(h))
		}
	}

	// Wrapping rather than failing. A build with more concurrent steps than the
	// segment holds would reuse an address while the first holder still had it,
	// which is worth knowing rather than worth guarding: the guest's own
	// concurrency is bounded far below this, and a guard would be untested code
	// standing in front of an impossibility.
	slot := i % len(free)
	host := free[slot]

	addr := netip.AddrFrom4([4]byte{base[0], base[1], base[2], host})

	return VMStepNet{
		Link:    fmt.Sprintf("es%d", slot),
		Addr:    addr,
		Gateway: gateway,
		Subnet:  subnet,
		// Locally administered and unicast, so it cannot collide with a real
		// card, and derived from the address so two steps cannot share one.
		MAC:  fmt.Sprintf("5a:94:ef:00:00:%02x", host),
		Kind: kind,
	}
}
