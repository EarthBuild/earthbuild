//go:build linux

package guest

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"

	"golang.org/x/sys/unix"
)

// defaultRouteMessage builds an RTM_NEWROUTE adding a default route via gw on
// the interface with index link.
//
// **Netlink is a protocol, not an ioctl.** The same route through `SIOCADDRT`
// means handing the kernel a struct pointer, which needs `unsafe`; as a
// netlink message it is a byte slice on a socket, which needs neither that nor
// a library. That matters here because a guest has no `ip` to shell out to and
// cannot grow one - the initramfs is two static Go binaries by design, and
// that minimalism is what makes it reproducible.
//
// Built by hand rather than with a library because this is the only netlink
// message the guest sends. Addresses and flags are ioctls, which x/sys/unix
// already wraps safely.
//
// seq is echoed in the kernel's acknowledgement, so a reply can be matched to
// the request that caused it.
func defaultRouteMessage(gw netip.Addr, link uint32, seq uint32) []byte {
	// Attributes are 4-byte aligned; a ragged one makes the kernel stop
	// reading and file a route with no gateway rather than refuse the message.
	const (
		attrHdr  = 4
		addrLen  = 4
		attrSize = attrHdr + addrLen // both fit exactly, so no padding is needed
	)

	msg := make([]byte, unix.SizeofNlMsghdr+unix.SizeofRtMsg+attrSize*2)

	total := len(msg)

	// The header: length first, because it is what the kernel reads to find
	// the end of this message in a stream of them.
	binary.NativeEndian.PutUint32(msg[0:4], uint32(total))
	binary.NativeEndian.PutUint16(msg[4:6], unix.RTM_NEWROUTE)
	binary.NativeEndian.PutUint16(msg[6:8],
		unix.NLM_F_REQUEST|unix.NLM_F_CREATE|unix.NLM_F_EXCL|unix.NLM_F_ACK)
	binary.NativeEndian.PutUint32(msg[8:12], seq)
	binary.NativeEndian.PutUint32(msg[12:16], 0) // to the kernel

	rt := msg[unix.SizeofNlMsghdr:]
	rt[0] = unix.AF_INET
	rt[1] = 0 // dst_len 0: everything, which is what makes it the default route
	rt[2] = 0 // src_len
	rt[3] = 0 // tos
	rt[4] = unix.RT_TABLE_MAIN
	rt[5] = unix.RTPROT_STATIC
	rt[6] = unix.RT_SCOPE_UNIVERSE
	rt[7] = unix.RTN_UNICAST

	at := unix.SizeofNlMsghdr + unix.SizeofRtMsg

	// RTA_GATEWAY: where to send what matches.
	binary.NativeEndian.PutUint16(msg[at:at+2], attrSize)
	binary.NativeEndian.PutUint16(msg[at+2:at+4], unix.RTA_GATEWAY)

	v4 := gw.As4()
	copy(msg[at+attrHdr:at+attrSize], v4[:])

	at += attrSize

	// RTA_OIF: which interface it leaves by. Without it the kernel picks from
	// the gateway's on-link route, which is right until a step has two.
	binary.NativeEndian.PutUint16(msg[at:at+2], attrSize)
	binary.NativeEndian.PutUint16(msg[at+2:at+4], unix.RTA_OIF)
	binary.NativeEndian.PutUint32(msg[at+attrHdr:at+attrSize], link)

	return msg
}

// netlinkPatience bounds the wait for the kernel's answer, in seconds.
//
// Generous, because this is a local socket and the kernel replies immediately
// or never: the number is here to turn "never" into an error rather than to
// express an expectation about how long a reply takes.
const netlinkPatience = 5

// addDefaultRoute installs a default route via gw on the named interface,
// inside whatever network namespace this thread is in.
func addDefaultRoute(gw netip.Addr, ifname string) error {
	idx, err := interfaceIndex(ifname)
	if err != nil {
		return err
	}

	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("open a netlink socket: %w", err)
	}

	defer func() { _ = unix.Close(fd) }()

	err = unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK})
	if err != nil {
		return fmt.Errorf("bind a netlink socket: %w", err)
	}

	// **A netlink read has to have a deadline.** The kernel answers a message
	// it understands, and simply does not answer one it cannot parse - so a
	// header this package got wrong is not an error, it is a wait with no end.
	// Found by deliberately corrupting the length: the test did not fail, it
	// hung, which is the worse of the two outcomes and the one a build would
	// have inherited.
	err = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO,
		&unix.Timeval{Sec: netlinkPatience})
	if err != nil {
		return fmt.Errorf("bound the wait for netlink's answer: %w", err)
	}

	const seq = 1

	err = unix.Sendto(fd, defaultRouteMessage(gw, idx, seq), 0,
		&unix.SockaddrNetlink{Family: unix.AF_NETLINK})
	if err != nil {
		return fmt.Errorf("ask for a default route via %s: %w", gw, err)
	}

	// **The acknowledgement is read, because netlink does not fail loudly.** A
	// refused request is an NLMSG_ERROR nobody has to collect, so a route that
	// was never added looks exactly like one that was until something tries to
	// use it.
	return readAck(fd, seq, "route")
}

// readAck reads the kernel's answer and turns a refusal into an error.
//
// what names the request, because this is shared: a link creation reported as
// "the kernel refused the route" sends a reader to the routing code for a
// failure in the link message, which cost one round of looking in the wrong
// place.
func readAck(fd int, seq uint32, what string) error {
	buf := make([]byte, os.Getpagesize())

	n, _, err := unix.Recvfrom(fd, buf, 0)
	if err != nil {
		if errors.Is(err, unix.EAGAIN) {
			return fmt.Errorf("netlink did not answer within %ds, which means it could not"+
				" parse the request: %w", netlinkPatience, err)
		}

		return fmt.Errorf("read netlink's answer: %w", err)
	}

	if n < unix.SizeofNlMsghdr+4 {
		return fmt.Errorf("netlink answered %d bytes, too short to be an acknowledgement", n)
	}

	if got := binary.NativeEndian.Uint16(buf[4:6]); got != unix.NLMSG_ERROR {
		// Anything else at this point is a message for somebody else.
		return nil
	}

	if got := binary.NativeEndian.Uint32(buf[8:12]); got != seq {
		return fmt.Errorf("netlink answered request %d, not %d", got, seq)
	}

	// The payload is a signed negative errno, and zero is the acknowledgement
	// of success - NLMSG_ERROR carries both.
	code := int32(binary.NativeEndian.Uint32(buf[unix.SizeofNlMsghdr : unix.SizeofNlMsghdr+4]))
	if code == 0 {
		return nil
	}

	return fmt.Errorf("the kernel refused the %s: %w", what, unix.Errno(-code))
}

// interfaceIndex is the kernel's number for a named interface.
func interfaceIndex(name string) (uint32, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return 0, fmt.Errorf("open a socket to look up %s: %w", name, err)
	}

	defer func() { _ = unix.Close(fd) }()

	req, err := unix.NewIfreq(name)
	if err != nil {
		return 0, fmt.Errorf("name %s: %w", name, err)
	}

	err = unix.IoctlIfreq(fd, unix.SIOCGIFINDEX, req)
	if err != nil {
		return 0, fmt.Errorf("look up the index of %s: %w", name, err)
	}

	return req.Uint32(), nil
}

// makeGuestTap creates a tap in the calling thread's network namespace.
//
// The same TUNSETIFF the host-side shim uses, on this side of the boundary: a
// step's own link is made where the step will run, so nothing has to be moved
// between namespaces - which is the operation that would have needed netlink.
func makeGuestTap(name string) error {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/net/tun: %w", err)
	}

	defer func() { _ = unix.Close(fd) }()

	req, err := unix.NewIfreq(name)
	if err != nil {
		return fmt.Errorf("name the tap %s: %w", name, err)
	}

	// IFF_NO_PI: bare Ethernet frames, with none of the four-byte header the
	// tun driver otherwise prepends. Both ends speak L2 and neither wants it.
	req.SetUint16(unix.IFF_TAP | unix.IFF_NO_PI)

	err = unix.IoctlIfreq(fd, unix.TUNSETIFF, req)
	if err != nil {
		return fmt.Errorf("make the tap %s: %w", name, err)
	}

	return unix.IoctlSetInt(fd, unix.TUNSETPERSIST, 1)
}
