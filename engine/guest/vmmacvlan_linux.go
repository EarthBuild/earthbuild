//go:build linux

package guest

import (
	"encoding/binary"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// macvlanModeBridge lets a parent's children reach each other as well as the
// world.
//
// From the kernel's `if_link.h`, which x/sys/unix does not export: private is
// 1, VEPA 2, bridge 4, passthru 8. Bridge, because two steps on one guest are
// peers on the segment - under private they could each reach the network and
// not each other, which is a difference nobody would predict from the outside.
const macvlanModeBridge = 4

// attr is one netlink attribute: a length, a kind, a payload, padded to four.
//
// **Padding is not counted in the length.** The header records the header plus
// the payload; the next attribute starts at the next four-byte boundary. Get
// that the other way round and the kernel reads a kind from the middle of a
// payload, which it reports as EINVAL with nothing said about where.
func attr(kind uint16, payload []byte) []byte {
	const hdr = 4

	size := hdr + len(payload)
	out := make([]byte, (size+3)&^3)

	binary.NativeEndian.PutUint16(out[0:2], uint16(size))
	binary.NativeEndian.PutUint16(out[2:4], kind)
	copy(out[hdr:], payload)

	return out
}

// attrU32 is an attribute holding one 32-bit value.
func attrU32(kind uint16, v uint32) []byte {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, v)

	return attr(kind, b)
}

// macvlanMessage builds an RTM_NEWLINK creating a macvlan on parent, inside the
// network namespace named by nsFD.
//
// **Created straight into the namespace, which is what avoids moving it.**
// IFLA_NET_NS_FD on the creating message puts the interface where it is wanted
// from the start; without it the interface appears here and then has to be
// moved, which is a second message and a window in which a step's link exists
// somewhere it should not.
//
// A macvlan rather than a veth pair and a bridge: the host's switch learns a
// source MAC per connection, so a child with its own MAC is simply another
// host on the segment the VM is already on. That is what makes this one
// message instead of a bridge, two links, addresses at both ends and NAT.
func macvlanMessage(n VMStepNet, parent uint32, nsFD int, seq uint32) []byte {
	mac, _ := parseMAC(n.MAC)

	// Innermost first: the mode sits inside INFO_DATA, which sits inside
	// LINKINFO beside INFO_KIND.
	data := attrU32(unix.IFLA_MACVLAN_MODE, macvlanModeBridge)
	kind := attr(unix.IFLA_INFO_KIND, []byte("macvlan\x00"))
	info := attr(unix.IFLA_LINKINFO, append(kind, attr(unix.IFLA_INFO_DATA, data)...))

	body := make([]byte, 0, 128)
	body = append(body, attrU32(unix.IFLA_LINK, parent)...)
	body = append(body, attr(unix.IFLA_IFNAME, []byte(n.Link+"\x00"))...)
	body = append(body, attr(unix.IFLA_ADDRESS, mac)...)
	body = append(body, attrU32(unix.IFLA_NET_NS_FD, uint32(nsFD))...)
	body = append(body, info...)

	msg := make([]byte, unix.SizeofNlMsghdr+unix.SizeofIfInfomsg+len(body))

	binary.NativeEndian.PutUint32(msg[0:4], uint32(len(msg)))
	binary.NativeEndian.PutUint16(msg[4:6], unix.RTM_NEWLINK)
	binary.NativeEndian.PutUint16(msg[6:8],
		unix.NLM_F_REQUEST|unix.NLM_F_CREATE|unix.NLM_F_EXCL|unix.NLM_F_ACK)
	binary.NativeEndian.PutUint32(msg[8:12], seq)

	// ifinfomsg is left zero but for the family: an interface being created has
	// no index yet, and flags are set afterwards by bringing it up.
	msg[unix.SizeofNlMsghdr] = unix.AF_UNSPEC

	copy(msg[unix.SizeofNlMsghdr+unix.SizeofIfInfomsg:], body)

	return msg
}

// parseMAC turns "5a:94:ef:00:00:03" into six bytes.
//
// Hand-rolled rather than net.ParseMAC so the failure is this package's: the
// addresses here are derived, not supplied, so a malformed one is a bug in
// vmStepNet and should read as one.
func parseMAC(s string) ([]byte, error) {
	out := make([]byte, 0, 6)

	for at := 0; at < len(s); at += 3 {
		if at+2 > len(s) {
			return nil, fmt.Errorf("malformed hardware address %q", s)
		}

		var b byte

		_, err := fmt.Sscanf(s[at:at+2], "%02x", &b)
		if err != nil {
			return nil, fmt.Errorf("malformed hardware address %q: %w", s, err)
		}

		out = append(out, b)
	}

	if len(out) != 6 {
		return nil, fmt.Errorf("hardware address %q is %d bytes, wanted 6", s, len(out))
	}

	return out, nil
}

// addMacvlan creates a step's interface inside the namespace held open by ns.
func addMacvlan(n VMStepNet, parent string, ns *os.File) error {
	idx, err := interfaceIndex(parent)
	if err != nil {
		return err
	}

	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("open a netlink socket: %w", err)
	}

	defer func() { _ = unix.Close(fd) }()

	err = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO,
		&unix.Timeval{Sec: netlinkPatience})
	if err != nil {
		return fmt.Errorf("bound the wait for netlink's answer: %w", err)
	}

	err = unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK})
	if err != nil {
		return fmt.Errorf("bind a netlink socket: %w", err)
	}

	const seq = 1

	err = unix.Sendto(fd, macvlanMessage(n, idx, int(ns.Fd()), seq), 0,
		&unix.SockaddrNetlink{Family: unix.AF_NETLINK})
	if err != nil {
		return fmt.Errorf("ask for %s on %s: %w", n.Link, parent, err)
	}

	return readAck(fd, seq)
}
