//go:build linux

package guest

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// uplink is the guest's own interface, whose segment a step's macvlan joins.
//
// Found rather than named, because a guest's NIC is called `eth0` until a
// kernel decides otherwise: the first interface that is up, not loopback and
// has an address is the one the host's switch is on.
func uplink() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("list this guest's interfaces: %w", err)
	}

	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := i.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}

		return i.Name, nil
	}

	return "", fmt.Errorf("this guest has no interface a step could share")
}

// configureIn addresses a step's interface from inside its namespace.
//
// **The ioctls have to be issued where the interface is.** An interface in
// another namespace is not in this one's table at all, so the address, the
// flags and the route are all set after entering - and the thread is locked
// and put back for the reason makeNetns gives: `setns` moves a thread, and one
// left behind answers a later step's syscalls in the wrong place.
func configureIn(nsPath string, n VMStepNet) (err error) {
	ns, err := os.Open(nsPath)
	if err != nil {
		return fmt.Errorf("open the namespace at %s: %w", nsPath, err)
	}

	defer func() { _ = ns.Close() }()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	here, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		return fmt.Errorf("find this thread's network namespace: %w", err)
	}

	defer func() { _ = here.Close() }()

	err = unix.Setns(int(ns.Fd()), unix.CLONE_NEWNET)
	if err != nil {
		return fmt.Errorf("enter the step's network namespace: %w", err)
	}

	defer func() {
		back := unix.Setns(int(here.Fd()), unix.CLONE_NEWNET)
		if back != nil && err == nil {
			err = fmt.Errorf("return this thread to its own network namespace: %w", back)
		}
	}()

	err = addressLink(n)
	if err != nil {
		return err
	}

	// Loopback too: a step that talks to itself - which is most of what a
	// nested daemon does - finds nothing listening without it, and the failure
	// reads as the daemon never starting.
	err = bringUpByName("lo")
	if err != nil {
		return fmt.Errorf("bring loopback up in the step's namespace: %w", err)
	}

	return addDefaultRoute(n.Gateway, n.Link)
}

// addressLink gives a step's interface its address, mask and flags.
func addressLink(n VMStepNet) error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return fmt.Errorf("open a socket to configure %s: %w", n.Link, err)
	}

	defer func() { _ = unix.Close(fd) }()

	req, err := unix.NewIfreq(n.Link)
	if err != nil {
		return fmt.Errorf("name %s: %w", n.Link, err)
	}

	addr := n.Addr.As4()

	err = req.SetInet4Addr(addr[:])
	if err != nil {
		return fmt.Errorf("set the address of %s: %w", n.Link, err)
	}

	err = unix.IoctlIfreq(fd, unix.SIOCSIFADDR, req)
	if err != nil {
		return fmt.Errorf("give %s the address %s: %w", n.Link, n.Addr, err)
	}

	mask := net.CIDRMask(n.Subnet.Bits(), 32)

	err = req.SetInet4Addr(mask)
	if err != nil {
		return fmt.Errorf("set the mask of %s: %w", n.Link, err)
	}

	err = unix.IoctlIfreq(fd, unix.SIOCSIFNETMASK, req)
	if err != nil {
		return fmt.Errorf("give %s its netmask: %w", n.Link, err)
	}

	return bringUpByName(n.Link)
}

// bringUpByName raises an interface in the calling thread's namespace.
func bringUpByName(name string) error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}

	defer func() { _ = unix.Close(fd) }()

	req, err := unix.NewIfreq(name)
	if err != nil {
		return err
	}

	err = unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, req)
	if err != nil {
		return fmt.Errorf("read the flags of %s: %w", name, err)
	}

	req.SetUint16(req.Uint16() | unix.IFF_UP | unix.IFF_RUNNING)

	err = unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, req)
	if err != nil {
		return fmt.Errorf("bring %s up: %w", name, err)
	}

	return nil
}

// nativeStepNet builds a step's network with no `ip` and no `iptables`.
//
// **The guest cannot do what the host does.** On a Linux host a private step
// network is a veth pair, a bridge and NAT, all built by shelling out to `ip`;
// a guest has neither program and cannot grow one - the initramfs is two static
// Go binaries, and that is what makes it reproducible.
//
// It does not need them. The host's switch learns a source MAC per connection,
// so a macvlan on the guest's own NIC is simply another host on the segment the
// VM is already on: its own MAC, its own address, its own ports. One netlink
// message, and ioctls for the rest.
//
// Returns the same three values as openStepNet, and for the same reason: a
// guest that cannot do this runs shared, which is what it did before.
func nativeStepNet(i int) (path string, done func(), why string) {
	nothing := func() {}

	parent, err := uplink()
	if err != nil {
		return "", nothing, err.Error()
	}

	n := vmStepNet(i)
	at := filepath.Join(netnsDir, n.Link)

	err = makeNetns(at)
	if err != nil {
		return "", nothing, fmt.Sprintf("a network namespace could not be made: %v", err)
	}

	release := func() { _ = removeNetns(at) }

	ns, err := os.Open(at)
	if err != nil {
		release()

		return "", nothing, fmt.Sprintf("the namespace at %s could not be opened: %v", at, err)
	}

	defer func() { _ = ns.Close() }()

	err = addMacvlan(n, parent, ns)
	if err != nil {
		release()

		return "", nothing, fmt.Sprintf("a step's interface on %s could not be made: %v", parent, err)
	}

	err = configureIn(at, n)
	if err != nil {
		release()

		return "", nothing, fmt.Sprintf("a step's interface could not be configured: %v", err)
	}

	return at, release, ""
}
