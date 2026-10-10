package vmboot_test

import (
	"net/netip"
	"strconv"
	"testing"

	"github.com/EarthBuild/earthbuild/cmd/earth-vmboot/vmboot"
)

// What the host encodes, the guest reads back.
//
// **One codec, used by both sides**, for the reason the ports are shared: the
// kernel command line is the only way a setting reaches a guest, and two
// spellings of it is a guest configured with nothing and no error anywhere.
func TestTheGuestReadsBackWhatTheHostWrote(t *testing.T) {
	t.Parallel()

	want := vmboot.Net{
		Address: netip.MustParsePrefix("172.30.0.2/30"),
		Gateway: netip.MustParseAddr("172.30.0.1"),
		DNS:     netip.MustParseAddr("1.1.1.1"),
	}

	got := vmboot.ParseNet("console=ttyS0 " + want.BootArgs() + " panic=1")

	if got != want {
		t.Errorf("the guest reads %+v, the host wrote %+v", got, want)
	}
}

// A guest booted with no network settings has none, rather than a zero address
// it would then configure an interface with.
func TestNoNetworkArgumentsMeanNoNetwork(t *testing.T) {
	t.Parallel()

	if got := vmboot.ParseNet("console=ttyS0 panic=1"); got.Wanted() {
		t.Errorf("a guest with no network arguments believes it has one: %+v", got)
	}
}

// The peer of a /30 is the other address in it, whichever end this is.
//
// A /30 is two usable addresses and exactly two, which is why the host's tap
// carrying one is enough to say what the guest's must be - no second setting,
// and no way for the two to disagree.
func TestThePeerIsTheOtherAddress(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ tap, want string }{
		{"172.30.0.1/30", "172.30.0.2"},
		{"172.30.0.2/30", "172.30.0.1"},
		{"10.0.0.5/30", "10.0.0.6"},
	} {
		got, err := vmboot.PeerOf(netip.MustParsePrefix(c.tap))
		if err != nil {
			t.Errorf("%s: %v", c.tap, err)

			continue
		}

		if got.String() != c.want {
			t.Errorf("the peer of %s is %s, wanted %s", c.tap, got, c.want)
		}
	}
}

// Anything but a /30 is refused, because the peer is only unambiguous there.
func TestOnlyASlashThirtyHasOnePeer(t *testing.T) {
	t.Parallel()

	if _, err := vmboot.PeerOf(netip.MustParsePrefix("192.168.1.10/24")); err == nil {
		t.Error("a /24 was accepted, and it has 253 peers rather than one")
	}
}

// The MAC follows from the address, so a guest keeps it across boots and two
// guests on one host cannot collide.
func TestTheMACFollowsTheAddress(t *testing.T) {
	t.Parallel()

	at := netip.MustParseAddr("172.30.0.2")

	first, second := vmboot.MACFor(at), vmboot.MACFor(at)
	if first != second {
		t.Errorf("two calls gave %s and %s", first, second)
	}

	// Locally administered and not multicast: the low two bits of the first
	// octet say so, and a kernel drops a multicast source address.
	lead, err := strconv.ParseUint(first[:2], 16, 8)
	if err != nil {
		t.Fatal(err)
	}

	if lead&0x02 == 0 || lead&0x01 != 0 {
		t.Errorf("%s is not a locally administered unicast address", first)
	}

	if vmboot.MACFor(netip.MustParseAddr("172.30.0.6")) == first {
		t.Error("two addresses share one MAC")
	}
}
