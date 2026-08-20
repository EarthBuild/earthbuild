package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
)

func anID(t *testing.T) key.EndpointID {
	t.Helper()

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("a key: %v", err)
	}

	return sk.Public().EndpointID()
}

func aRelay(t *testing.T) netaddr.RelayURL {
	t.Helper()

	u, err := netaddr.ParseRelayURL("https://relay.example/")
	if err != nil {
		t.Fatalf("a relay url: %v", err)
	}

	return u
}

// Dialling an identity means looking it up first.
//
// `Endpoint.Connect` tries the addresses *in* the EndpointAddr it is given and
// no others: the configured lookup services add addresses to a remote that is
// already known, and are not consulted to start a dial. So an EndpointAddr
// carrying only an id - which is all a worker derives from the shared secret -
// fails immediately with `no reachable address for endpoint`, however well
// discovery is working (E505).
//
// *Configured is not consulted.* Registering a resolver on an endpoint says
// where lookups may go, not that anything will look.
func TestAnIdentityWithNoAddressIsLookedUp(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id, relay := anID(t), aRelay(t)

	known := iroh.NewMemoryLookup()
	known.AddEndpointInfo(dns.EndpointInfo{
		ID:   id,
		Data: dns.NewEndpointData(netaddr.RelayAddr{URL: relay}),
	})

	services := &iroh.AddressLookupServices{}
	services.AddResolver(known)

	r := &Reachable{services: services}

	got, err := r.Find(ctx, netaddr.NewEndpointAddr(id))
	if err != nil {
		t.Fatalf("looking up an identity that is published: %v", err)
	}

	if len(got.Addrs()) == 0 {
		t.Fatalf("resolved nothing for an identity a resolver knows"+
			"\n  Connect would refuse this with no reachable address")
	}
}

// Being told where to go beats looking it up.
//
// A worker given EARTH_FLEET_DRIVER already knows; paying a DNS round trip to
// re-learn it would make the fast path the slow one.
func TestAnIdentityWithAnAddressIsNotLookedUp(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id := anID(t)

	// A resolver that would fail if it were asked.
	empty := iroh.NewMemoryLookup()
	services := &iroh.AddressLookupServices{}
	services.AddResolver(empty)

	r := &Reachable{services: services}

	told := netaddr.NewEndpointAddr(id).WithRelayURL(aRelay(t))

	got, err := r.Find(ctx, told)
	if err != nil {
		t.Fatalf("an address that was given should need no lookup: %v", err)
	}

	if len(got.Addrs()) != 1 {
		t.Errorf("%d address(es), want the one it was told", len(got.Addrs()))
	}
}

// Without discovery, an address is whatever it already was.
//
// The nil Reachable stays the off switch: a fleet on one LAN passes addresses
// through untouched and never reaches a resolver.
func TestFindingWithoutDiscoveryChangesNothing(t *testing.T) {
	t.Parallel()

	var r *Reachable

	want := netaddr.NewEndpointAddr(anID(t))

	got, err := r.Find(context.Background(), want)
	if err != nil {
		t.Fatalf("discovery is off and finding failed: %v", err)
	}

	if got.String() != want.String() {
		t.Errorf("got %v, want %v unchanged", got, want)
	}
}
