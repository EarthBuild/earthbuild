//go:build linux

package exec

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

// EnvTap names the tap device a microVM's guest reaches the network through.
//
// **Pre-created, because creating one needs a privilege a build must not
// have.** `TUNSETIFF` on a new device wants CAP_NET_ADMIN, and so does giving
// it an address or a route - so the engine takes a device somebody made once,
// rather than asking every build to run as root. See docs/native/settings.md
// for the three commands that make one.
const EnvTap = "EARTH_VM_TAP"

// defaultTap is the device looked for when nothing names one.
//
// A name rather than nothing, so a machine that has been set up works with no
// settings at all, and one that has not says what is missing.
const defaultTap = "earthtap0"

// netFor is the guest's configuration, derived from the tap's own.
//
// **One setting, not two.** The tap carries one of the two usable addresses in
// a /30 and the guest takes the other; a second setting for the guest's address
// would be a second thing to keep in step, and the failure when they drifted
// would be a guest with a route to nowhere.
func netFor(tap netip.Prefix, dns netip.Addr) (vmboot.Net, error) {
	guestAt, err := vmboot.PeerOf(tap)
	if err != nil {
		return vmboot.Net{}, fmt.Errorf("%w"+
			"\n  give the tap a /30: `ip addr add 172.30.0.1/30 dev %s`", err, defaultTap)
	}

	return vmboot.Net{
		Address: netip.PrefixFrom(guestAt, tap.Bits()),
		Gateway: tap.Addr(),
		DNS:     dns,
	}, nil
}

// tapNet reads the named tap's address, and says what is missing when it cannot.
//
// Unprivileged: reading an interface's addresses needs nothing, which is the
// whole reason the device is made in advance rather than here.
func tapNet(name string) (netip.Prefix, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("no tap device %s on this machine: %w"+
			"\n  a microVM reaches the network through one, and making it needs a"+
			" privilege a build must not have - see %s in docs/native/settings.md",
			name, err, EnvTap)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("read the addresses of %s: %w", name, err)
	}

	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.To4() == nil {
			continue
		}

		at, ok := netip.AddrFromSlice(n.IP.To4())
		if !ok {
			continue
		}

		ones, _ := n.Mask.Size()

		return netip.PrefixFrom(at, ones), nil
	}

	return netip.Prefix{}, fmt.Errorf("the tap device %s has no IPv4 address"+
		"\n  `ip addr add 172.30.0.1/30 dev %s`", name, name)
}

// firstReachable is the first nameserver in a resolv.conf that a guest could
// actually use.
//
// Loopback is dropped for the reason `reachableNameservers` drops it: 127.0.0.53
// names a listener in *this* machine's namespace, and the guest's loopback is
// its own and empty. A guest given it has a resolv.conf that looks right,
// resolves nothing, and fails as `apk add … exited 1, and printed nothing`.
func firstReachable(conf string) netip.Addr {
	for _, s := range guest.ReachableNameservers(conf) {
		at, err := netip.ParseAddr(s)
		if err == nil && at.Is4() {
			return at
		}
	}

	return netip.Addr{}
}

// hostResolver is the resolver this machine uses, as one address a guest can
// reach through the NAT.
func hostResolver() netip.Addr {
	// systemd-resolved's own file first, for the reason the guest prefers it:
	// /etc/resolv.conf there holds only the stub on 127.0.0.53, and the servers
	// it forwards to are in the other file.
	for _, at := range []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"} {
		b, err := os.ReadFile(at) //nolint:gosec // two fixed paths
		if err != nil {
			continue
		}

		if got := firstReachable(string(b)); got.IsValid() {
			return got
		}
	}

	return netip.Addr{}
}

// guestNet is the network this sandbox gives its guest, and the reason there is
// none when there is none.
//
// Absent is not an error: a guest without a network still builds, and what it
// cannot do is fetch. The reason is returned so the caller can say it once,
// rather than leaving a step to fail on a name that will not resolve.
func guestNet() (vmboot.Net, string) {
	name := os.Getenv(EnvTap)
	if name == "" {
		name = defaultTap
	}

	if strings.EqualFold(name, "off") {
		return vmboot.Net{}, ""
	}

	tap, err := tapNet(name)
	if err != nil {
		return vmboot.Net{}, err.Error()
	}

	net, err := netFor(tap, hostResolver())
	if err != nil {
		return vmboot.Net{}, err.Error()
	}

	return net, ""
}
