package fleet_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
)

// A peer address survives being written down and read back.
//
// It is the one piece of a holder hint that crosses machines as text: a worker
// announces itself, the driver passes the string on, and a third machine dials
// it. Two identities are in there - who, and where - and losing either makes the
// dial fail closed rather than connect to the wrong machine, because iroh
// verifies the endpoint identity during the handshake.
func TestAPeerAddressRoundTrips(t *testing.T) {
	t.Parallel()

	id, err := fleet.DriverID(fleet.Session{Session: "s"}, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	at := fleet.PeerAddr{ID: id, Host: "192.0.2.7:41000"}

	got, err := fleet.ParsePeerAddr(at.String())
	if err != nil {
		t.Fatalf("%v", err)
	}

	if got.ID != at.ID || got.Host != at.Host {
		t.Errorf("read back %+v, want %+v", got, at)
	}
}

// Rubbish is refused, not half-parsed.
//
// The string arrives from another machine (A5). A parser that accepted a
// truncated identity would dial something - and the something it dialled would
// be whatever the handshake let through.
func TestABadPeerAddressIsRefused(t *testing.T) {
	t.Parallel()

	id, err := fleet.DriverID(fleet.Session{Session: "s"}, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	real := id.String()

	for _, s := range []string{
		"",
		"no-at-sign",
		"@192.0.2.7:41000",
		"notanidentity@192.0.2.7:41000",
		"deadbeef@",
		// A real identity and nowhere to go. The one the identity parser
		// cannot catch, because the half it checks is perfectly good - and
		// dialling a host of "" is a dial to whatever the default is.
		real + "@",
		// A real identity and no separator at all: the whole string reads as
		// an identity, and the host is silently nothing.
		real,
	} {
		_, err := fleet.ParsePeerAddr(s)
		if err == nil {
			t.Errorf("%q was accepted as a peer address", s)
		}
	}
}
