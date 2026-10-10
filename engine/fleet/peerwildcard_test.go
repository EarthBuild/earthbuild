package fleet

import (
	"testing"

	"github.com/tmc/go-iroh/key"
)

// A peer that says it is on every interface has said nothing.
//
// A worker binds its blob endpoint to the wildcard and reports
// `<id>@[::]:50406`. On one machine that is harmless - the dial lands on
// loopback, which is where the peer actually is. Across machines it resolves at
// the dialler to the *dialler's* own loopback, so a fetch goes nowhere near the
// worker holding the layer, and the build stalls with a joined worker that
// produces nothing (E505).
//
// An address with no host left is not an error: it is an identity, which is
// enough for a fleet with discovery to look the peer up. Failing here instead
// would refuse the one configuration this is for.
func TestAPeerOnEveryInterfaceIsAPeerNowhere(t *testing.T) {
	t.Parallel()

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("a key: %v", err)
	}

	id := sk.Public().EndpointID()

	for _, host := range []string{"[::]:50406", "0.0.0.0:50406"} {
		at, err := (PeerAddr{ID: id, Host: host}).Endpoint()
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}

		if !at.IsEmpty() {
			t.Errorf("%s became %v"+
				"\n  a peer elsewhere would dial its own machine", host, at.Addrs())
		}

		if !at.ID.Equal(id) {
			t.Errorf("%s lost the identity, which is the part worth keeping", host)
		}
	}
}

// A real host is still a real host.
func TestAPeerWithAnAddressKeepsIt(t *testing.T) {
	t.Parallel()

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("a key: %v", err)
	}

	at, err := (PeerAddr{ID: sk.Public().EndpointID(), Host: "10.1.2.3:50406"}).Endpoint()
	if err != nil {
		t.Fatalf("a routable host: %v", err)
	}

	if len(at.Addrs()) != 1 {
		t.Errorf("%d address(es), want the one it was given", len(at.Addrs()))
	}
}
