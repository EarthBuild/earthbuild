package fleet

import (
	"os"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/relay"
)

// EnvDiscover turns on relays and endpoint discovery for a fleet.
//
// **Opt-in, and that is a retreat rather than a design.** It was on by default
// for one increment; a worker given the driver's address - the path that had
// been doing real work an hour earlier - joined and was then given nothing. The
// mechanism changes how endpoints are addressed and something downstream of that
// stopped working, and defaulting a build tool onto a path whose failure I have
// not explained is not a trade worth making (E505).
//
// What it is for: machines that cannot dial each other, which is every pair of
// CI runners. A fleet on one LAN needs none of it.
const EnvDiscover = "EARTH_FLEET_DISCOVER"

// endpointOptions are what makes a fleet endpoint reachable from another
// machine.
//
// **go-iroh binds direct-only.** `WithRelayMode`'s own documentation says the
// default is `relay.ModeDisabled`, and nothing configures endpoint discovery -
// so a peer holding a node id and no address got `no reachable address for
// endpoint`, and a fleet could only form between machines that could already
// dial each other (E505).
//
// That is a property of this binding rather than of the design. Rust iroh has
// both on by default, which is how a throwaway cluster forms out of N GitHub
// runners that have no route to each other and nothing to tell each other.
//
// Applied only where a fleet was asked for: a driver binding because workers
// were wanted, and a worker joining. An ordinary local build binds no endpoint,
// so nothing here reaches the network for a build that has no fleet.
// **Resolving is not enough; somebody has to publish.** The first version added
// only a DNS resolver, and a lookup of an identity nobody had announced returned
// nothing: `no reachable address for endpoint`, exactly as before. A driver
// publishes where it can be reached, and that needs its secret key - which is
// why this takes one rather than being a fixed list.
func endpointOptions(sk key.SecretKey) []iroh.Option {
	if os.Getenv(EnvDiscover) == "" {
		return nil
	}

	lookup := &iroh.AddressLookupServices{}

	// Reading: what somebody else published about themselves.
	lookup.AddResolver(iroh.NewDNSAddressLookup(dns.N0DNSEndpointOriginProd, nil))

	// Writing: where this endpoint can be reached. Fire-and-forget, and a
	// failure to publish leaves an endpoint that can still be dialled directly -
	// so it is not a reason to refuse to start.
	if pub, err := iroh.N0PkarrPublisher(sk, nil); err == nil {
		lookup.AddPublisher(pub)
	}

	return []iroh.Option{
		// Relays carry a connection where hole punching does not land, which is
		// the case two NAT'd CI runners are in.
		iroh.WithRelayMode(relay.ModeDefault()),
		iroh.WithAddressLookup(lookup),
	}
}

// EndpointOptions is endpointOptions for callers outside this package - a worker
// binds its own endpoint and needs the same reachability the driver has.
func EndpointOptions(sk key.SecretKey) []iroh.Option { return endpointOptions(sk) }
