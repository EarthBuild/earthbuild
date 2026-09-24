package guest

import (
	"net/netip"
	"testing"
)

// theMicroVMs is the segment this engine's own microVM switch is on. The
// existing tests below were written against it when it was a constant in the
// package; it is passed in now, which is the whole of the fix they describe.
var (
	theMicroVMs  = netip.MustParsePrefix("192.168.127.0/24")
	theGuestsOwn = netip.MustParseAddr("192.168.127.2")
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

	// Fewer than the subnet holds, so uniqueness is a real claim here.
	for i := range 64 {
		n := vmStepNetOn(i, theMicroVMs, theGuestsOwn)

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

// No step is given an address that is already spoken for.
//
// Steps are macvlan children on the guest's own NIC, so they join the segment
// the VM is already on rather than getting a subnet of their own. That is what
// makes a second TCP/IP stack in the guest unnecessary - and it is why the
// allocator has to know what is already there.
func TestTheGuestSwitchDoesNotOverlapTheHosts(t *testing.T) {
	t.Parallel()

	// Steps sit on the segment the VM is already on, so what must be avoided is
	// not the subnet but the addresses already spoken for: the gateway at .1
	// and the guest's own NIC at .2.
	taken := map[string]string{"192.168.127.1": "the gateway", "192.168.127.2": "the guest's own NIC"}

	for i := range 300 {
		n := vmStepNetOn(i, theMicroVMs, theGuestsOwn)
		if who, ok := taken[n.Addr.String()]; ok {
			t.Fatalf("step %d was given %s (%s)", i, n.Addr, who)
		}
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
		if n := vmStepNetOn(i, theMicroVMs, theGuestsOwn); len(n.Link) >= 16 {
			t.Errorf("step %d gets interface name %q, which is %d bytes and will be refused",
				i, n.Link, len(n.Link))
		}
	}
}

// **The segment a step joins is the one its parent is on, not a constant.**
//
// A macvlan makes a step another host on the segment the guest's NIC is already
// on - so its address has to come from that NIC, and the subnet cannot be
// written down here. It was: `vmStepSpace` named 192.168.127.0/24, which is what
// this engine's own microVM uses, and `uplink` read the parent's address and
// threw it away.
//
// On a backend whose VM sits elsewhere the step is then addressed onto a segment
// that does not exist. Measured on Apple's `container`, whose VM is on
// 192.168.64.0/24: the step came up on 192.168.127.87 with a default route via
// 192.168.127.1, and every connection - including one to a literal address, so
// not a DNS problem - returned "Host is unreachable".
func TestAStepJoinsTheSegmentItsParentIsOn(t *testing.T) {
	t.Parallel()

	for _, one := range []struct {
		what   string
		parent netip.Prefix
		own    netip.Addr
	}{
		{
			"this engine's microVM", netip.MustParsePrefix("192.168.127.0/24"),
			netip.MustParseAddr("192.168.127.2"),
		},
		{
			"Apple's container", netip.MustParsePrefix("192.168.64.0/24"),
			netip.MustParseAddr("192.168.64.3"),
		},
		{
			"a /16", netip.MustParsePrefix("10.201.0.0/16"),
			netip.MustParseAddr("10.201.0.9"),
		},
	} {
		t.Run(one.what, func(t *testing.T) {
			t.Parallel()

			seen := map[netip.Addr]bool{}

			for i := range 64 {
				n := vmStepNetOn(i, one.parent, one.own)

				if !one.parent.Contains(n.Addr) {
					t.Fatalf("step %d was given %s, which is not in %s", i, n.Addr, one.parent)
				}

				if n.Addr == one.own {
					t.Fatalf("step %d was given the guest's own address", i)
				}

				if n.Addr == n.Gateway {
					t.Fatalf("step %d was given the gateway's address", i)
				}

				if seen[n.Addr] {
					t.Fatalf("step %d reused %s", i, n.Addr)
				}

				seen[n.Addr] = true

				if n.Subnet != one.parent {
					t.Fatalf("step %d sits in %s, want %s", i, n.Subnet, one.parent)
				}
			}
		})
	}
}

// The gateway is the first address of the segment, which is what both backends
// put there - and what `resolv.conf` named on the one this was found on.
func TestTheGatewayIsTheFirstAddressOfTheSegment(t *testing.T) {
	t.Parallel()

	for _, one := range []struct{ subnet, want string }{
		{"192.168.127.0/24", "192.168.127.1"},
		{"192.168.64.0/24", "192.168.64.1"},
		{"10.201.0.0/16", "10.201.0.1"},
	} {
		n := vmStepNetOn(0, netip.MustParsePrefix(one.subnet), netip.MustParseAddr(one.want))
		if n.Gateway.String() != one.want {
			t.Errorf("on %s the gateway is %s, want %s", one.subnet, n.Gateway, one.want)
		}
	}
}
