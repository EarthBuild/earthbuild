//go:build linux

package guest

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

// The macvlan request is a well-formed netlink message with its attributes
// nested correctly.
//
// **Nesting is where a hand-built RTM_NEWLINK goes wrong.** IFLA_LINKINFO
// contains IFLA_INFO_KIND and IFLA_INFO_DATA, and IFLA_INFO_DATA contains the
// mode - three levels, each with a length covering everything inside it. Get
// an outer length wrong and the kernel reads the inner attributes as siblings,
// finds no kind, and answers EOPNOTSUPP: "operation not supported", which
// sounds like the kernel lacks macvlan rather than like this package cannot
// count.
//
// Addressed by PID rather than by an open descriptor, which is what lets the
// agent stay where it is: a process that can see the parent NIC creates the
// interface directly inside another process's namespace, so no thread of this
// one ever moves. See RunStepNetShimIfAsked.
//
// Checked as bytes because that error names nothing, and because the live
// check needs a real parent NIC - a step's macvlan hangs off the guest's own
// interface, which no unprivileged test namespace has.
func TestTheMacvlanRequestNestsItsAttributes(t *testing.T) {
	t.Parallel()

	const (
		parent = 2
		nsPID  = 4242
	)

	n := vmStepNet(0)
	msg := macvlanMessage(n, parent, nsPID, 1)

	if got := binary.NativeEndian.Uint32(msg[0:4]); int(got) != len(msg) {
		t.Fatalf("the header says %d bytes and the message is %d", got, len(msg))
	}

	if got := binary.NativeEndian.Uint16(msg[4:6]); got != unix.RTM_NEWLINK {
		t.Errorf("message type is %d, wanted RTM_NEWLINK (%d)", got, unix.RTM_NEWLINK)
	}

	if len(msg)%4 != 0 {
		t.Errorf("the message is %d bytes and netlink attributes are 4-byte aligned", len(msg))
	}

	attrs := msg[unix.SizeofNlMsghdr+unix.SizeofIfInfomsg:]

	want := map[uint16]bool{
		unix.IFLA_LINK:       false,
		unix.IFLA_IFNAME:     false,
		unix.IFLA_ADDRESS:    false,
		unix.IFLA_NET_NS_PID: false,
		unix.IFLA_LINKINFO:   false,
	}

	var linkinfo []byte

	for at := 0; at+4 <= len(attrs); {
		size := int(binary.NativeEndian.Uint16(attrs[at : at+2]))
		kind := binary.NativeEndian.Uint16(attrs[at+2 : at+4])

		if size < 4 || at+size > len(attrs) {
			t.Fatalf("attribute at %d claims %d bytes, which does not fit", at, size)
		}

		want[kind] = true

		if kind == unix.IFLA_LINKINFO {
			linkinfo = attrs[at+4 : at+size]
		}

		at += (size + 3) &^ 3
	}

	for kind, seen := range want {
		if !seen {
			t.Errorf("attribute %d is missing", kind)
		}
	}

	// The kind is what tells the kernel this is a macvlan at all.
	if !bytes.Contains(linkinfo, []byte("macvlan")) {
		t.Error("IFLA_LINKINFO does not name the macvlan kind, so the kernel will refuse it")
	}

	// And the mode, nested one deeper: bridge, so children can reach each
	// other as well as the world - two steps on one guest are peers on the
	// segment, not strangers.
	var mode bool

	for at := 0; at+4 <= len(linkinfo); {
		size := int(binary.NativeEndian.Uint16(linkinfo[at : at+2]))
		kind := binary.NativeEndian.Uint16(linkinfo[at+2 : at+4])

		if size < 4 || at+size > len(linkinfo) {
			t.Fatalf("nested attribute at %d claims %d bytes, which does not fit", at, size)
		}

		if kind == unix.IFLA_INFO_DATA {
			data := linkinfo[at+4 : at+size]
			if len(data) >= 8 && binary.NativeEndian.Uint16(data[2:4]) == unix.IFLA_MACVLAN_MODE {
				mode = binary.NativeEndian.Uint32(data[4:8]) == macvlanModeBridge
			}
		}

		at += (size + 3) &^ 3
	}

	if !mode {
		t.Error("the macvlan mode is not bridge, so two steps could not see each other")
	}
}
