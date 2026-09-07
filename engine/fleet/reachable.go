package fleet

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
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

// Reachable is how a fleet endpoint is found by a machine that cannot dial it.
//
// **go-iroh binds direct-only.** `WithRelayMode`'s own documentation says the
// default is `relay.ModeDisabled`, and nothing configures endpoint discovery -
// so a peer holding an endpoint id and no address got `no reachable address for
// endpoint`, and a fleet could only form between machines that could already
// dial each other (E505). That is a property of this binding rather than of the
// design: Rust iroh has both on by default, which is how a throwaway cluster
// forms out of N CI runners with no route to each other.
//
// A nil *Reachable is discovery turned off, and both methods work on one. Every
// call site can then bind and announce unconditionally, rather than repeating
// the check four times and getting it right three.
type Reachable struct {
	services *iroh.AddressLookupServices
}

// Discovery configures reachability for an endpoint with this key, or returns
// nil if the fleet did not ask for it.
//
// It takes a secret key because publishing is signed: an endpoint's announcement
// of where it is has to be provably from that endpoint, or anyone could move
// anyone's traffic.
func Discovery(sk key.SecretKey) *Reachable {
	if os.Getenv(EnvDiscover) == "" {
		return nil
	}

	services := &iroh.AddressLookupServices{}

	// Reading: what somebody else published about themselves.
	services.AddResolver(iroh.NewDNSAddressLookup(dns.N0DNSEndpointOriginProd, nil))

	// Writing: where this endpoint can be reached. A failure to configure it
	// leaves an endpoint that can still be dialled directly, so it is not a
	// reason to refuse to start.
	pub, err := iroh.N0PkarrPublisher(sk, nil)
	if err == nil {
		services.AddPublisher(pub)
	}

	return &Reachable{services: services}
}

// Options are the bind options that make an endpoint discoverable.
func (r *Reachable) Options() []iroh.Option {
	if r == nil {
		return nil
	}

	return []iroh.Option{
		// Relays carry a connection where hole punching does not land, which is
		// the case two NAT'd CI runners are in.
		iroh.WithRelayMode(relay.ModeDefault()),
		// Without a net report an endpoint behind NAT knows only its bind
		// address, which is `[::]:port` - "every interface on this host", which
		// resolves at a peer to that peer's own loopback. It would have nothing
		// true to publish about itself.
		iroh.WithNetReport(),
		iroh.WithAddressLookup(r.services),
	}
}

// Announce publishes where e can be reached, and keeps publishing as that
// changes.
//
// **Binding with a publisher attached does not publish.** Nothing in go-iroh
// calls `Publish`; `endpoint.go` only ever resolves. So the first version of
// this configured both halves, published nothing, and every worker looked up an
// identity that had never been announced - which reads as `no reachable address
// for endpoint`, indistinguishable from an endpoint that is simply not there
// (E505).
func (r *Reachable) Announce(ctx context.Context, e *iroh.Endpoint) {
	r.announce(ctx, e, time.Second)
}

// addressed is the part of an endpoint that says where it is.
//
// An interface for one method, because the bug is in *when* the answer changes
// and a test needs an address that appears without saying so.
type addressed interface {
	Addr() netaddr.EndpointAddr
}

// announce polls rather than subscribing.
//
// **`WatchAddr` does not fire for the address that matters.** `Addr()` is
// composed from the bind address, external NAT candidates and the home relay;
// `updateAddrWatchLocked` runs on the NAT paths and on InsertRelay/RemoveRelay,
// and never when a home relay is elected. Behind NAT the relay is the only
// dialable address an endpoint has, so subscribing means waiting for a
// notification that is never sent - which is how this stayed broken through two
// fixes that both looked right (E505).
//
// Polling also covers the case a subscription cannot: an address set that is
// empty for the first seconds of an endpoint's life, which is every endpoint.
func (r *Reachable) announce(ctx context.Context, e addressed, every time.Duration) {
	if r == nil {
		return
	}

	go func() {
		var said string

		for {
			if addr := e.Addr(); !addr.IsEmpty() && addr.String() != said {
				said = addr.String()

				r.services.Publish(dns.NewEndpointData(addr.Addrs()...))
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(every):
			}
		}
	}()
}

// Find fills in where an identity can be reached, if it needs filling in.
//
// **`Connect` does not consult the lookup services to start a dial.** Its own
// documentation says it tries the direct addresses in the EndpointAddr and then
// the relay URLs in the EndpointAddr; the resolver an endpoint is bound with
// adds addresses to a remote it is already talking to. So an identity with no
// address - which is exactly what a worker derives from the shared secret -
// fails with `no reachable address for endpoint` no matter how healthy discovery
// is (E505).
//
// *Configured is not consulted.* Registering a resolver says where lookups may
// go, not that anything will look.
//
// An address that was given is left alone: a worker told EARTH_FLEET_DRIVER
// already knows, and a DNS round trip to re-learn it would make the fast path
// the slow one.
func (r *Reachable) Find(ctx context.Context, addr netaddr.EndpointAddr) (netaddr.EndpointAddr, error) {
	if r == nil || !addr.IsEmpty() {
		return addr, nil
	}

	found := addr

	for item, err := range r.services.Resolve(ctx, addr.ID) {
		if err != nil {
			continue
		}

		found = found.WithAddrs(item.Addr().Addrs()...)
	}

	if found.IsEmpty() {
		return addr, fmt.Errorf("%w: nothing is published for %v"+
			"\n  a driver publishes where it is a second or two after it starts,"+
			" and the record takes a few more to become resolvable",
			iroh.ErrNoAddress, addr.ID)
	}

	return found, nil
}
