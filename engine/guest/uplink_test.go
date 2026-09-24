package guest

import (
	"errors"
	"net"
	"testing"
)

func iface(name string, flags net.Flags) net.Interface {
	return net.Interface{Name: name, Flags: flags}
}

func at(cidr string) net.Addr {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}

	ip, _, _ := net.ParseCIDR(cidr)
	n.IP = ip

	return n
}

// **The segment has to come back with the name.** Reading the addresses to
// choose an interface and then discarding them is what left the caller naming a
// subnet of its own, and that subnet was this engine's microVM's - so a step on
// any other backend was addressed onto a segment with no route off it.
func TestTheUplinkCarriesTheSegmentItIsOn(t *testing.T) {
	t.Parallel()

	up, err := uplinkAmong(
		[]net.Interface{
			iface("lo", net.FlagUp|net.FlagLoopback),
			iface("eth9", 0), // down
			iface("eth0", net.FlagUp),
		},
		func(i net.Interface) ([]net.Addr, error) {
			if i.Name == "eth0" {
				return []net.Addr{at("192.168.64.3/24")}, nil
			}

			return nil, nil
		})
	if err != nil {
		t.Fatalf("no uplink found: %v", err)
	}

	if up.Name != "eth0" {
		t.Errorf("chose %q", up.Name)
	}

	if up.Subnet.String() != "192.168.64.0/24" {
		t.Errorf("segment is %s, want 192.168.64.0/24", up.Subnet)
	}

	if up.Addr.String() != "192.168.64.3" {
		t.Errorf("the guest's own address is %s", up.Addr)
	}
}

// Loopback is never it, and neither is an interface that is down or has no
// address - a step hung off any of them has nowhere to send anything.
func TestTheUplinkSkipsWhatCannotCarryAStep(t *testing.T) {
	t.Parallel()

	_, err := uplinkAmong(
		[]net.Interface{
			iface("lo", net.FlagUp|net.FlagLoopback),
			iface("eth0", 0),
			iface("eth1", net.FlagUp),
		},
		func(net.Interface) ([]net.Addr, error) { return nil, nil })

	if !errors.Is(err, errNoUplink) {
		t.Errorf("with nothing usable: %v", err)
	}
}

// **An IPv6-only interface is not an uplink.** A macvlan on one needs an
// allocator this does not have, and choosing it would produce a step that
// cannot be addressed at all - which reads as a broken network rather than as
// an unsupported one.
func TestTheUplinkSkipsAnInterfaceWithNoIPv4(t *testing.T) {
	t.Parallel()

	up, err := uplinkAmong(
		[]net.Interface{iface("eth0", net.FlagUp), iface("eth1", net.FlagUp)},
		func(i net.Interface) ([]net.Addr, error) {
			if i.Name == "eth0" {
				return []net.Addr{at("fd00::1/64")}, nil
			}

			return []net.Addr{at("10.0.0.5/16")}, nil
		})
	if err != nil {
		t.Fatalf("no uplink found: %v", err)
	}

	if up.Name != "eth1" || up.Subnet.String() != "10.0.0.0/16" {
		t.Errorf("chose %q on %s", up.Name, up.Subnet)
	}
}
