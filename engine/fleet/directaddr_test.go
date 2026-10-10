package fleet

import (
	"net/netip"
	"testing"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
)

// TestTheDirectAddressIsTakenFromTheValidatedPath.
//
// **A worker cannot dial a peer directly because it is never told where the
// peer is.** The address a worker announces is a wildcard - `<id>@[::]:40682` -
// so the only place a routable address appears is on the connection, once the
// endpoints have observed each other. That is where this takes it from, so a
// second connection can be made with nothing but that address in it and no
// relay to fall back to (E-F1, GitHub).
func TestTheDirectAddressIsTakenFromTheValidatedPath(t *testing.T) {
	t.Parallel()

	url, err := netaddr.ParseRelayURL("https://relay.example/")
	if err != nil {
		t.Fatal(err)
	}

	relayed := iroh.PathInfo{
		Validated: true, HasAddr: true, Addr: netaddr.RelayAddr{URL: url},
	}

	want := netip.MustParseAddrPort("74.235.90.91:28737")
	direct := iroh.PathInfo{
		Validated: true, HasAddr: true, Addr: netaddr.IPAddr{Addr: want},
	}

	if _, ok := directAddr([]iroh.PathInfo{relayed}); ok {
		t.Error("a relay was offered as somewhere to dial directly")
	}

	got, ok := directAddr([]iroh.PathInfo{relayed, direct})
	if !ok {
		t.Fatal("a validated direct path yielded no address to dial")
	}

	if got != want {
		t.Errorf("dialling %v, want %v", got, want)
	}

	// Probing is not reachable yet, and dialling it would replace a working
	// relay connection with one that may never come up.
	probing := direct
	probing.Validated = false

	if _, ok := directAddr([]iroh.PathInfo{probing}); ok {
		t.Error("an unvalidated path was offered as somewhere to dial")
	}
}
