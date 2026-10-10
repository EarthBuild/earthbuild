//go:build linux

package guest

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"golang.org/x/sys/unix"
)

// The default route is a netlink message this package builds itself.
//
// **Netlink is a protocol, not an ioctl.** Adding a route with `SIOCADDRT`
// means handing the kernel a struct pointer, which needs `unsafe`; the same
// route as an `RTM_NEWROUTE` message is a byte slice on an `AF_NETLINK` socket,
// which needs neither that nor a library. A guest has no `ip` to shell out to
// and cannot grow one - the initramfs is two static Go binaries by design.
//
// Checked as bytes because the kernel's answer to a malformed message is
// EINVAL with nothing said about which field, and a header off by four bytes
// reads exactly like a permissions problem.
func TestADefaultRouteIsAWellFormedNetlinkMessage(t *testing.T) {
	t.Parallel()

	gw := netip.MustParseAddr("10.202.0.1")

	msg := defaultRouteMessage(gw, 7, 1)

	if len(msg) < unix.SizeofNlMsghdr+unix.SizeofRtMsg {
		t.Fatalf("the message is %d bytes, shorter than a header and a route", len(msg))
	}

	// The length the kernel reads first, and the one a hand-built message gets
	// wrong first.
	if got := binary.NativeEndian.Uint32(msg[0:4]); int(got) != len(msg) {
		t.Errorf("the header says %d bytes and the message is %d", got, len(msg))
	}

	if got := binary.NativeEndian.Uint16(msg[4:6]); got != unix.RTM_NEWROUTE {
		t.Errorf("message type is %d, wanted RTM_NEWROUTE (%d)", got, unix.RTM_NEWROUTE)
	}

	flags := binary.NativeEndian.Uint16(msg[6:8])
	for name, want := range map[string]uint16{
		"NLM_F_REQUEST": unix.NLM_F_REQUEST,
		"NLM_F_CREATE":  unix.NLM_F_CREATE,
		"NLM_F_ACK":     unix.NLM_F_ACK,
	} {
		if flags&want == 0 {
			t.Errorf("the request does not set %s, so the kernel will not %s", name,
				map[string]string{
					"NLM_F_REQUEST": "treat it as a request",
					"NLM_F_CREATE":  "create the route",
					"NLM_F_ACK":     "say whether it worked",
				}[name])
		}
	}

	// A default route is dst_len 0 - the whole point - and every attribute must
	// be padded to four bytes or the kernel stops reading at the first ragged
	// one and reports a route with no gateway.
	rt := msg[unix.SizeofNlMsghdr:]
	if rt[1] != 0 {
		t.Errorf("dst_len is %d, wanted 0 for a default route", rt[1])
	}

	if len(msg)%4 != 0 {
		t.Errorf("the message is %d bytes and netlink attributes are 4-byte aligned", len(msg))
	}
}
