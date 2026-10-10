package fleet

import (
	"net/netip"
	"testing"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
)

// TestADirectPathIsRecognised.
//
// **Two GitHub runners in the same datacentre fetched through us-west-1.** The
// blob connection came up on a relay, the transfer started on it, and 7.9 MiB
// took 6.213s - about 1.2 MiB/s between machines whose network does orders of
// magnitude better:
//
//	earth-worker: fetching from f246144c… over
//	  relay:https://usw1-1.relay.n0.iroh-canary.iroh.link./
//
// A relay is the fallback that makes a fleet work at all where hole punching
// cannot land (E505). It is not where a transfer should live when a direct path
// is one round of punching away.
func TestADirectPathIsRecognised(t *testing.T) {
	t.Parallel()

	url, err := netaddr.ParseRelayURL("https://relay.example/")
	if err != nil {
		t.Fatal(err)
	}

	relayed := iroh.PathInfo{
		Validated: true, HasAddr: true, Addr: netaddr.RelayAddr{URL: url},
	}

	direct := iroh.PathInfo{
		Validated: true, HasAddr: true,
		Addr: netaddr.IPAddr{Addr: netip.MustParseAddrPort("10.1.0.4:41234")},
	}

	if directIn([]iroh.PathInfo{relayed}) {
		t.Error("a relay was taken for a direct path, so nothing would ever wait" +
			" for one and every transfer stays on the detour")
	}

	if !directIn([]iroh.PathInfo{relayed, direct}) {
		t.Error("a hole-punched path beside a relay was not recognised, so the" +
			" wait runs its full length on a connection that is already direct")
	}

	// Probing is not carrying. Waiting must not stop on a path that cannot yet
	// take application data, or the transfer starts on the relay anyway.
	probing := direct
	probing.Validated = false

	if directIn([]iroh.PathInfo{probing}) {
		t.Error("an unvalidated path ended the wait")
	}

	if directIn(nil) {
		t.Error("a connection with no paths reported a direct one")
	}
}
