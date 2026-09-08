package guest

import (
	"fmt"
	"net/netip"
)

// vmStepSpace is where a microVM's per-step networks are addressed from.
//
// **Not the host's range.** The host runs a virtual switch for the VM's single
// NIC on 192.168.127.0/24; a guest-side switch sharing it would give a step a
// route to the other side of the boundary and a gateway that is two different
// machines depending which table answered.
//
// A /16 of the same private block the host's `private` mode avoided
// buildkit's 172.30.0.0/16 for, and distinct from that mode's 10.201.0.0/16:
// all three can be live on one machine while the comparison this branch exists
// for is being made.
const vmStepSpace = "10.202.0.0/16"

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

	// .1 is the switch; steps start at .2, so no step is ever handed the
	// gateway's own address.
	const gatewayHost = 1

	block := i & 0x3fff
	host := block + gatewayHost + 1

	base := subnet.Addr().As4()
	gw := netip.AddrFrom4([4]byte{base[0], base[1], 0, gatewayHost})
	addr := netip.AddrFrom4([4]byte{base[0], base[1], byte(host >> 8), byte(host)})

	return VMStepNet{
		Link:    fmt.Sprintf("es%d", block),
		Addr:    addr,
		Gateway: gw,
		Subnet:  subnet,
		MAC:     fmt.Sprintf("5a:94:ef:%02x:%02x:%02x", 0, host>>8, host&0xff),
	}
}
