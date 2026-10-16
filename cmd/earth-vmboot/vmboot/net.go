package vmboot

import (
	"crypto/sha256"
	"fmt"
	"net/netip"
	"strings"
)

// Net is a guest's whole network configuration.
//
// **Carried on the kernel command line**, because that is the only channel into
// a guest that exists before the guest is running: there is no shell, no
// profile and no shared filesystem, and the agent's own connection arrives
// later than the interface is needed.
//
// One codec, used by the host that writes it and the PID 1 that reads it, for
// the reason the ports are shared: two spellings would be a guest configured
// with nothing and no error anywhere.
type Net struct {
	// Address is the guest's own address and the prefix it sits in.
	Address netip.Prefix
	// Gateway is the host end of the pair, which is the tap device.
	Gateway netip.Addr
	// DNS is one resolver, reached through the gateway. One rather than the
	// host's whole list, because a guest behind a /30 and a NAT reaches them
	// all the same way and the first that answers is the answer.
	DNS netip.Addr
}

// argIP is the kernel's own IP autoconfiguration parameter.
//
// **The kernel's, not one of ours.** `CONFIG_IP_PNP` reads this before `/init`
// runs and configures the interface itself, so the guest needs no `ip` binary,
// no ioctls and no netlink - three values on the command line instead of a
// second copy of iproute2 in the initramfs.
//
//	ip=<client>:<server>:<gateway>:<netmask>:<hostname>:<device>:<autoconf>:<dns0>
//
// The empty fields are the ones only an NFS root uses.
const argIP = "ip="

// Wanted reports whether this guest was given a network at all.
//
// A guest without one still builds; what it cannot do is fetch. Saying so is
// the difference between a step that fails with a name that will not resolve
// and a sandbox that quietly has no route.
func (n Net) Wanted() bool {
	return n.Address.IsValid() && n.Address.Bits() > 0 && n.Gateway.IsValid()
}

// BootArgs renders this configuration for the kernel command line.
func (n Net) BootArgs() string {
	if !n.Wanted() {
		return ""
	}

	dns := ""
	if n.DNS.IsValid() {
		dns = n.DNS.String()
	}

	return fmt.Sprintf("%s%s::%s:%s::%s:off:%s", argIP,
		n.Address.Addr(), n.Gateway, netmask(n.Address.Bits()), Iface, dns)
}

// ParseNet reads a configuration out of a kernel command line.
//
// Anything it cannot parse is absent rather than an error: this runs in PID 1
// of a guest that has already booted, and a malformed argument must leave a
// machine that says it has no network rather than one that will not start.
func ParseNet(cmdline string) Net {
	var out Net

	for _, field := range strings.Fields(cmdline) {
		if !strings.HasPrefix(field, argIP) {
			continue
		}

		// client:server:gateway:netmask:hostname:device:autoconf:dns0
		f := strings.Split(strings.TrimPrefix(field, argIP), ":")
		if len(f) < 4 {
			continue
		}

		at, err := netip.ParseAddr(f[0])
		if err != nil {
			continue
		}

		out.Address = netip.PrefixFrom(at, bitsOf(f[3]))
		out.Gateway, _ = netip.ParseAddr(f[2])

		if len(f) > 7 {
			out.DNS, _ = netip.ParseAddr(f[7])
		}
	}

	return out
}

// Iface is the one interface a microVM has. The machine configuration gives it
// exactly one and the kernel names them in order.
const Iface = "eth0"

// netmask renders a prefix length the way the kernel parameter wants it.
func netmask(bits int) string {
	var m [4]byte

	for i := range 32 {
		if i < bits {
			m[i/8] |= 1 << (7 - i%8)
		}
	}

	return netip.AddrFrom4(m).String()
}

// bitsOf is netmask backwards, and answers 0 for anything it cannot read -
// which `Wanted` then reports as no network, rather than a prefix of nowhere.
func bitsOf(mask string) int {
	at, err := netip.ParseAddr(mask)
	if err != nil || !at.Is4() {
		return 0
	}

	b := at.As4()
	n := 0

	for i := range 32 {
		if b[i/8]&(1<<(7-i%8)) == 0 {
			break
		}

		n++
	}

	return n
}

// PeerOf is the other address in a /30.
//
// **A /30 and only a /30**, because that is the size at which "the other one"
// is a definition rather than a guess: four addresses, of which one is the
// network and one the broadcast, leaving exactly two. The host's tap carries
// one, so the guest's follows from it - no second setting to keep in step, and
// no way for the two ends to disagree about who is where.
func PeerOf(tap netip.Prefix) (netip.Addr, error) {
	if !tap.Addr().Is4() || tap.Bits() != 30 {
		return netip.Addr{}, fmt.Errorf("%s is not an IPv4 /30, so it has no single peer"+
			"\n  the tap carries one of the two usable addresses and the guest"+
			" takes the other, which is only unambiguous at /30", tap)
	}

	b := tap.Addr().As4()

	// The two usable addresses are network+1 and network+2, so each is the
	// other's peer: whichever end the tap holds, flipping the low bit gives the
	// other. `& 3` isolates the position within the /30.
	switch b[3] & 3 {
	case 1:
		b[3]++
	case 2:
		b[3]--
	default:
		return netip.Addr{}, fmt.Errorf("%s is the network or broadcast address of its /30"+
			"\n  give the tap one of the two usable addresses", tap.Addr())
	}

	return netip.AddrFrom4(b), nil
}

// MACFor is the hardware address a guest at this address is given.
//
// **Derived rather than random**, so a guest keeps it across boots: a changing
// MAC is a new interface to anything on the host that remembers one, and two
// guests deriving from two addresses cannot collide.
//
// Locally administered and unicast, which the first octet says: bit 1 set marks
// it as not vendor-assigned, and bit 0 clear keeps it out of multicast, where a
// kernel would drop it as a source address.
func MACFor(at netip.Addr) string {
	sum := sha256.Sum256([]byte(at.String()))

	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		(sum[0]|0x02)&^byte(0x01), sum[1], sum[2], sum[3], sum[4], sum[5])
}
