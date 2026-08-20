package fleet

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
)

// Whether an endpoint that announced itself can then be found by its identity
// alone, which is the whole of what a worker needs to join.
//
// Skipped unless a fleet asked for discovery, because it reaches n0's
// infrastructure and a unit test must not. It is a probe rather than a
// guarantee: it can show the mechanism working and cannot show it working from
// somebody else's network.
func TestDiscoveryReachesTheWorld(t *testing.T) { //nolint:paralleltest // network
	if os.Getenv(EnvDiscover) == "" {
		t.Skipf("set %s to run this; it reaches the network", EnvDiscover)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("a key: %v", err)
	}

	found := Discovery(sk)

	e, err := iroh.Bind(ctx, append([]iroh.Option{iroh.WithSecretKey(sk)}, found.Options()...)...)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	defer func() { _ = e.Shutdown(context.Background()) }()

	found.Announce(ctx, e)

	// What it has to say about itself, once it has anything to say.
	var addr string

	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); {
		if a := e.Addr(); !a.IsEmpty() {
			addr = a.String()
			break
		}

		time.Sleep(250 * time.Millisecond)
	}

	t.Logf("endpoint says it is at: %q", addr)

	if addr == "" {
		t.Fatalf("the endpoint never learned an address to publish"+
			"\n  with no relay and no net report there is nothing true to say")
	}

	time.Sleep(5 * time.Second) // publication is fire-and-forget

	// A resolver that knows nothing but the identity, which is a worker.
	var asked iroh.AddressLookupServices
	asked.AddResolver(iroh.NewDNSAddressLookup(dns.N0DNSEndpointOriginProd, nil))

	n := 0

	for item, rerr := range asked.Resolve(ctx, e.ID()) {
		if rerr != nil {
			if errors.Is(rerr, iroh.ErrNoResults) {
				t.Errorf("resolved nothing: %v", rerr)
				continue
			}

			t.Logf("resolver said: %v", rerr)

			continue
		}

		n++

		t.Logf("resolved via %s: %v", item.Provenance(), item.Addr().Addrs())
	}

	if n == 0 {
		t.Fatalf("published %q and resolved nothing back"+
			"\n  the publisher is registered and the resolver is registered;"+
			" between them nothing arrives", addr)
	}
}

// Whether a particular endpoint - a driver started elsewhere - can be found.
//
// A diagnostic, not a guarantee: it takes an identity from the environment and
// says whether anything about it is resolvable. It separates a driver that never
// published from a worker that cannot resolve, which look identical from a
// worker's log.
func TestResolveThisEndpoint(t *testing.T) { //nolint:paralleltest // network
	want := os.Getenv("EARTH_PROBE_ID")
	if want == "" {
		t.Skip("set EARTH_PROBE_ID to the endpoint id to look up")
	}

	id, err := key.ParseEndpointID(want)
	if err != nil {
		t.Fatalf("%q is not an endpoint id: %v", want, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var asked iroh.AddressLookupServices
	asked.AddResolver(iroh.NewDNSAddressLookup(dns.N0DNSEndpointOriginProd, nil))

	n := 0

	for item, rerr := range asked.Resolve(ctx, id) {
		if rerr != nil {
			t.Logf("resolver said: %v", rerr)
			continue
		}

		n++

		t.Logf("resolved via %s: %v", item.Provenance(), item.Addr().Addrs())
	}

	if n == 0 {
		t.Fatalf("nothing resolvable for %s: it never published", want)
	}
}
