package fleet

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/tmc/go-iroh/dns"
	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
	"github.com/tmc/go-iroh/netaddr"
)

// saidWhere is what the pkarr publisher does, without the DNS: it records what
// an endpoint announced about itself.
type saidWhere struct {
	mu   sync.Mutex
	said []dns.EndpointData
}

func (s *saidWhere) Publish(d dns.EndpointData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.said = append(s.said, d)
}

func (s *saidWhere) latest() (dns.EndpointData, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.said) == 0 {
		return dns.EndpointData{}, 0
	}

	return s.said[len(s.said)-1], len(s.said)
}

// An endpoint that is discoverable has to say where it is.
//
// The bug this holds shut: the first version of discovery registered a resolver
// and a publisher and stopped there, on the assumption that binding an endpoint
// with a publisher attached would publish. Nothing in go-iroh calls Publish -
// `endpoint.go` only ever calls Resolve - so every worker looked up an identity
// that had never been announced and got `no reachable address for endpoint`
// (E505).
//
// *A registered publisher is not a publication.* The two are one word apart and
// the failure is silent: an endpoint with a publisher attached and nothing
// published looks exactly like one that is simply unreachable.
func TestAnAnnouncedEndpointSaysWhereItIs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// A *specified* bind address, because an endpoint bound to the wildcard has
	// nothing dialable to say about itself until a relay or a NAT report gives
	// it one - and both of those need the network. Loopback keeps this test
	// offline while still exercising the publish path.
	e, err := iroh.Bind(ctx, iroh.WithBindAddr(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)))
	if err != nil {
		t.Fatalf("bind an endpoint: %v", err)
	}
	defer func() { _ = e.Shutdown(context.Background()) }()

	heard := &saidWhere{}
	services := &iroh.AddressLookupServices{}
	services.AddPublisher(heard)

	r := &Reachable{services: services}
	r.Announce(ctx, e)

	var said dns.EndpointData

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d, n := heard.latest(); n > 0 && len(d.Addrs()) > 0 {
			said = d
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	if len(said.Addrs()) == 0 {
		_, n := heard.latest()
		t.Fatalf("an announced endpoint published %d time(s) and named no address"+
			"\n  a peer resolving its identity would find nothing", n)
	}

	if want := e.Addr(); len(want.Addrs()) > 0 && len(said.Addrs()) == 0 {
		t.Errorf("published %v, endpoint is at %v", said.Addrs(), want.Addrs())
	}
}

// Announcing is safe on a fleet that did not ask for discovery.
//
// The nil Reachable is the off switch, so every call site can announce
// unconditionally rather than repeating the check four times - and getting it
// right three times out of four.
func TestAnnouncingWithoutDiscoveryDoesNothing(t *testing.T) {
	t.Parallel()

	var r *Reachable

	if got := r.Options(); len(got) != 0 {
		t.Errorf("discovery is off and %d bind option(s) were applied", len(got))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	e, err := iroh.Bind(ctx)
	if err != nil {
		t.Fatalf("bind an endpoint: %v", err)
	}
	defer func() { _ = e.Shutdown(context.Background()) }()

	r.Announce(ctx, e) // must not panic
}

// Discovery is off unless asked for, and complete when it is.
//
// Off by default because turning it on regressed the direct-dial path that was
// working; see EnvDiscover. Complete when on, because a resolver without a
// publisher is the E505 bug and a publisher without a resolver is its mirror.
func TestDiscoveryIsOffUnlessAskedFor(t *testing.T) { //nolint:paralleltest // t.Setenv
	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("a key: %v", err)
	}

	t.Setenv(EnvDiscover, "")

	if r := Discovery(sk); r != nil {
		t.Errorf("%s is unset and discovery was configured anyway", EnvDiscover)
	}

	t.Setenv(EnvDiscover, "1")

	r := Discovery(sk)
	if r == nil {
		t.Fatalf("%s is set and discovery was not configured", EnvDiscover)
	}

	if got := len(r.Options()); got != 3 {
		t.Errorf("%d bind option(s), want a relay mode, a net report and an address lookup", got)
	}

	if got := r.services.Len(); got != 2 {
		t.Errorf("%d lookup service(s), want one that publishes and one that resolves"+
			"\n  a resolver with nothing publishing is E505", got)
	}
}

// An endpoint that has nothing dialable to say stays quiet.
//
// Publishing an empty record is worse than publishing nothing: it is a signed
// statement that this identity is at no address, and a resolver that finds one
// has an answer rather than a reason to keep looking. An endpoint's address is
// empty for the first moments of its life - the relay assignment and the NAT
// report both land later - so this is the normal state at bind time, not an
// error.
func TestAnEndpointWithNoAddressAnnouncesNothing(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The wildcard: bound, running, and with no address a peer could use.
	e, err := iroh.Bind(ctx)
	if err != nil {
		t.Fatalf("bind an endpoint: %v", err)
	}

	defer func() { _ = e.Shutdown(context.Background()) }()

	heard := &saidWhere{}
	services := &iroh.AddressLookupServices{}
	services.AddPublisher(heard)

	r := &Reachable{services: services}
	r.Announce(ctx, e)

	time.Sleep(200 * time.Millisecond)

	if d, n := heard.latest(); n > 0 && len(d.Addrs()) == 0 {
		t.Errorf("published an empty record %d time(s)"+
			"\n  a resolver finding one stops looking, which is worse than finding nothing", n)
	}
}

// where is an endpoint whose address appears without telling anybody.
type where struct {
	mu    sync.Mutex
	addr  netaddr.EndpointAddr
	asked int
}

func (w *where) Addr() netaddr.EndpointAddr {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked++

	return w.addr
}

func (w *where) becomes(a netaddr.EndpointAddr) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addr = a
}

// An address that arrives quietly is still published.
//
// This is the bug that outlived two fixes. `Endpoint.Addr()` is composed from
// three sources - the bind address, external NAT candidates, and the home relay
// - and `WatchAddr` notifies on the first two only: `updateAddrWatchLocked` is
// called from the NAT paths and from InsertRelay/RemoveRelay, never when a home
// relay is *elected*. On a machine behind NAT the relay is the only usable
// address, so the address a peer needs is exactly the one whose arrival is
// silent, and an Announce that trusted the watcher published nothing at all
// (E505).
//
// *A watcher that does not watch everything its value is derived from.* The
// remedy is not a better subscription; it is to stop subscribing and ask.
func TestAnAddressThatArrivesQuietlyIsStillPublished(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("a key: %v", err)
	}

	quiet := &where{} // empty, and no notification will ever be sent

	heard := &saidWhere{}
	services := &iroh.AddressLookupServices{}
	services.AddPublisher(heard)

	r := &Reachable{services: services}
	r.announce(ctx, quiet, time.Millisecond)

	if _, n := heard.latest(); n != 0 {
		t.Fatalf("published %d time(s) before there was anything to say", n)
	}

	relay, err := netaddr.ParseRelayURL("https://relay.example/")
	if err != nil {
		t.Fatalf("a relay url: %v", err)
	}

	quiet.becomes(netaddr.NewEndpointAddr(sk.Public().EndpointID()).WithRelayURL(relay))

	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if d, n := heard.latest(); n > 0 && len(d.Addrs()) > 0 {
			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatalf("an address appeared with no notification and was never published"+
		"\n  asked for it %d time(s)", quiet.asked)
}

// The same address is not published over and over.
//
// Publication is signed and goes to a third party; repeating an unchanged record
// every poll would be a request per poll, forever, for every endpoint in every
// fleet.
func TestAnUnchangedAddressIsPublishedOnce(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatalf("a key: %v", err)
	}

	relay, err := netaddr.ParseRelayURL("https://relay.example/")
	if err != nil {
		t.Fatalf("a relay url: %v", err)
	}

	steady := &where{addr: netaddr.NewEndpointAddr(sk.Public().EndpointID()).WithRelayURL(relay)}

	heard := &saidWhere{}
	services := &iroh.AddressLookupServices{}
	services.AddPublisher(heard)

	r := &Reachable{services: services}
	r.announce(ctx, steady, time.Millisecond)

	time.Sleep(200 * time.Millisecond)

	if _, n := heard.latest(); n != 1 {
		t.Errorf("published an unchanged address %d time(s), want 1"+
			"\n  it was polled %d time(s) in the same period", n, steady.asked)
	}
}
