//go:build linux

package guest

import (
	"net"
	"net/netip"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// The kernel accepts the route this package builds.
//
// **A hand-built netlink message is right or it is EINVAL**, and EINVAL says
// nothing about which field was wrong - a header off by four bytes reads
// exactly like a permissions problem. So the message is put to the kernel
// rather than only inspected.
//
// In a user namespace with a network namespace inside it, which needs no root:
// CAP_NET_ADMIN in a namespace you own is enough to make a tap, address it and
// route through it, and that is exactly the position the step shim is in.
func TestTheKernelAcceptsTheDefaultRoute(t *testing.T) {
	if os.Getenv("EARTH_ROUTE_CHILD") == "" {
		reexecForRoute(t)

		return
	}

	// Inside the namespaces now.
	n := vmStepNet(0)

	err := makeGuestTap(n.Link)
	if err != nil {
		t.Fatalf("make the tap: %v", err)
	}

	err = addressInterface(n)
	if err != nil {
		t.Fatalf("address it: %v", err)
	}

	err = addDefaultRoute(n.Gateway, n.Link)
	if err != nil {
		t.Fatalf("the kernel refused the route this package built: %v", err)
	}

	// The route is there if the kernel will now pick this interface for an
	// address outside the subnet.
	iface, err := net.InterfaceByName(n.Link)
	if err != nil {
		t.Fatalf("the interface went missing: %v", err)
	}

	if iface.Flags&net.FlagUp == 0 {
		t.Error("the interface is not up")
	}
}

// reexecForRoute runs this test again inside a user and network namespace.
func reexecForRoute(t *testing.T) {
	t.Helper()

	if _, err := os.Stat("/dev/net/tun"); err != nil {
		t.Skipf("no /dev/net/tun here: %v", err)
	}

	cmd := exec.Command("/proc/self/exe", "-test.run", t.Name(), "-test.v")
	cmd.Env = append(os.Environ(), "EARTH_ROUTE_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		UidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getuid(), Size: 1},
		},
		GidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getgid(), Size: 1},
		},
		GidMappingsEnableSetgroups: false,
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("in a namespace: %v\n%s", err, out)
	}
}

// addressInterface gives an interface its address and mask and brings it up.
func addressInterface(n VMStepNet) error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}

	defer func() { _ = unix.Close(fd) }()

	req, err := unix.NewIfreq(n.Link)
	if err != nil {
		return err
	}

	addr := n.Addr.As4()
	if err := req.SetInet4Addr(addr[:]); err != nil {
		return err
	}

	if err := unix.IoctlIfreq(fd, unix.SIOCSIFADDR, req); err != nil {
		return err
	}

	mask := netip.MustParseAddr("255.255.0.0").As4()
	if err := req.SetInet4Addr(mask[:]); err != nil {
		return err
	}

	if err := unix.IoctlIfreq(fd, unix.SIOCSIFNETMASK, req); err != nil {
		return err
	}

	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, req); err != nil {
		return err
	}

	req.SetUint16(req.Uint16() | unix.IFF_UP | unix.IFF_RUNNING)

	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, req)
}
