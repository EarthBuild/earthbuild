package guest

import (
	"fmt"
	"net/netip"
)

// vmStepSpace is where a microVM's per-step networks are addressed from.
//
// **The host switch's own subnet, deliberately.** An earlier draft of this put
// steps on a private range behind a second virtual switch running in the
// agent - which works, and costs about 8MB of TCP/IP stack in an initramfs
// that is 3.8MB in total and reproducible because it holds nothing else.
//
// It is not needed. The host's switch keeps a CAM table and learns a source
// MAC per connection, so many MACs on the VM's single link are forwarded
// correctly. A step given a macvlan on the guest's own NIC therefore appears
// as another host on the segment the VM is already on: its own MAC, its own
// address, its own port space, and no stack, bridge, veth or NAT anywhere.
//
// Which means steps share this subnet rather than getting one of their own,
// and the addresses have to avoid what is already on it: .1 is the gateway and
// .2 is the guest itself.
const vmStepSpace = "192.168.127.0/24"

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
	// collide on one.
	MAC string
}

// vmStepNet derives a step's network from its number.
//
// Pure, so the arithmetic is testable without a kernel: the failures that
// matter here are two steps given one address, a name too long for IFNAMSIZ,
// and a range that overlaps the host's - none of which needs a namespace to
// demonstrate.
//
// 16384 steps, wrapping. A build with more concurrent steps than that would
// reuse an address while the first holder still had it, which is worth knowing
// rather than worth guarding: the guest's own concurrency is bounded far below
// it, and a guard would be untested code standing in front of an impossibility.
func vmStepNet(i int) VMStepNet {
	subnet := netip.MustParsePrefix(vmStepSpace)

	// .1 is the gateway and .2 is the guest's own NIC, so steps start at .3.
	const (
		gatewayHost = 1
		firstStep   = 3
	)

	// A /24 with three addresses spoken for. Wrapping rather than failing: the
	// guest's concurrency is bounded far below this, and a guard would be
	// untested code in front of an impossibility.
	block := i % (254 - firstStep)
	host := block + firstStep

	base := subnet.Addr().As4()
	gw := netip.AddrFrom4([4]byte{base[0], base[1], base[2], gatewayHost})
	addr := netip.AddrFrom4([4]byte{base[0], base[1], base[2], byte(host)})

	return VMStepNet{
		Link:    fmt.Sprintf("es%d", block),
		Addr:    addr,
		Gateway: gw,
		Subnet:  subnet,
		// Locally administered and unicast, so it cannot collide with a real
		// card, and derived from the address so two steps cannot share one.
		MAC: fmt.Sprintf("5a:94:ef:00:00:%02x", host),
	}
}
