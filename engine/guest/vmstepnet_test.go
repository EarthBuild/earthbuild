package guest

import (
	"net/netip"
	"testing"
)

// Each step gets its own address on the guest's own switch.
//
// **A microVM guest has one NIC and cannot be given another while it runs**, so
// the veth-and-NAT arrangement `ip` builds on a Linux host is not available
// inside one: creating a veth pair needs netlink, and the initramfs holds two
// static Go binaries and no `ip`.
//
// What is available is a second virtual switch, running in the agent, with a
// tap per step created inside that step's own network namespace - a `TUNSETIFF`
// ioctl and no netlink at all. The switch forwards outbound by dialling from
// the guest, which leaves by the one NIC the guest does have.
//
// Every step therefore needs a distinct address on that switch. Without one,
// steps share a namespace and two nested daemons wanting port 8371 collide -
// which is eight of the microVM's test failures and nothing else.
func TestEachStepGetsItsOwnAddressOnTheGuestSwitch(t *testing.T) {
	t.Parallel()

	seen := map[netip.Addr]int{}

	for i := range 64 {
		n := vmStepNet(i)

		if !n.Addr.IsValid() {
			t.Fatalf("step %d got no address", i)
		}

		if prev, ok := seen[n.Addr]; ok {
			t.Fatalf("steps %d and %d were both given %s", prev, i, n.Addr)
		}

		seen[n.Addr] = i

		if n.Addr == n.Gateway {
			t.Errorf("step %d was given the gateway's own address %s", i, n.Addr)
		}

		if !n.Subnet.Contains(n.Addr) || !n.Subnet.Contains(n.Gateway) {
			t.Errorf("step %d: %s and gateway %s are not both in %s", i, n.Addr, n.Gateway, n.Subnet)
		}
	}
}

// The guest's own switch does not overlap the host's.
//
// The host runs one of these for the VM's single NIC on 192.168.127.0/24. A
// guest-side switch on the same range would give a step a route to its own
// side of the boundary and a gateway that is two different machines depending
// which table answered.
func TestTheGuestSwitchDoesNotOverlapTheHosts(t *testing.T) {
	t.Parallel()

	hosts := netip.MustParsePrefix("192.168.127.0/24")

	n := vmStepNet(0)
	if n.Subnet.Overlaps(hosts) {
		t.Errorf("the guest's step network %s overlaps the host's %s", n.Subnet, hosts)
	}
}

// A name the kernel will take.
//
// IFNAMSIZ is 16 including the terminator, and a name a byte too long is
// refused at TUNSETIFF with EINVAL - which reads as "the device could not be
// created" and sends the reader looking at permissions.
func TestTheInterfaceNameFits(t *testing.T) {
	t.Parallel()

	for _, i := range []int{0, 9, 10, 999, 16383} {
		if n := vmStepNet(i); len(n.Link) >= 16 {
			t.Errorf("step %d gets interface name %q, which is %d bytes and will be refused",
				i, n.Link, len(n.Link))
		}
	}
}
