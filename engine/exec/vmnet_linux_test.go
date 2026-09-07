//go:build linux

package exec

import (
	"net/netip"
	"strings"
	"testing"
)

// The guest's address follows from the tap's, so there is one setting and not
// two that can disagree.
func TestTheGuestTakesTheOtherHalfOfTheTapsPrefix(t *testing.T) {
	t.Parallel()

	got, err := netFor(netip.MustParsePrefix("172.30.0.1/30"), netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Fatal(err)
	}

	if got.Address.String() != "172.30.0.2/30" {
		t.Errorf("the guest is at %s", got.Address)
	}

	if got.Gateway.String() != "172.30.0.1" {
		t.Errorf("the gateway is %s, and it has to be the tap", got.Gateway)
	}
}

// A tap configured with anything but a /30 is refused, and the message says
// what to do: the whole point of the /30 is that it needs no second setting.
func TestATapWithoutASlashThirtyIsRefusedWithAdvice(t *testing.T) {
	t.Parallel()

	_, err := netFor(netip.MustParsePrefix("192.168.1.10/24"), netip.Addr{})
	if err == nil {
		t.Fatal("a /24 tap was accepted")
	}

	if !strings.Contains(err.Error(), "/30") {
		t.Errorf("the refusal does not say what is wanted: %v", err)
	}
}

// A resolver on loopback is not carried into the guest.
//
// **127.0.0.53 names a listener on the host**, and the guest's loopback is its
// own and empty. A guest given it has a resolv.conf that looks right, resolves
// nothing, and fails as `apk add … exited 1, and printed nothing` - which is
// E931, arriving through a different door.
func TestALoopbackResolverIsNotCarriedIn(t *testing.T) {
	t.Parallel()

	got := firstReachable("nameserver 127.0.0.53\nnameserver 9.9.9.9\n")

	if got.String() != "9.9.9.9" {
		t.Errorf("the guest was given %s", got)
	}
}

// No reachable resolver is no resolver, rather than a zero address written into
// the guest's resolv.conf.
func TestNoReachableResolverIsNone(t *testing.T) {
	t.Parallel()

	if got := firstReachable("nameserver 127.0.0.53\n"); got.IsValid() {
		t.Errorf("a loopback resolver was carried in as %s", got)
	}
}
