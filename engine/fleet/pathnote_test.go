package fleet

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/netaddr"
)

// TestAFetchSaysWhetherItWentDirect.
//
// **A relay is a detour through the public internet, and nothing said when one
// was taken.** Two GitHub runners in the same datacentre moved 7.9 MiB in
// 6.366s - 1.24 MiB/s, which is not what that network does - and the log had no
// way to distinguish a hole-punched path from a connection bouncing off a relay
// on another continent. Relays are configured deliberately, because two NAT'd
// runners may have no other option (E505); knowing which one a build got is the
// difference between tuning a transport and tuning a route.
//
// Validated paths only: a probing path is one being tried, not one carrying the
// bytes, and reporting it would name a route the transfer never used.
func TestAFetchSaysWhetherItWentDirect(t *testing.T) {
	t.Parallel()

	direct := iroh.PathInfo{
		Validated: true, HasAddr: true,
		Addr:   netaddr.IPAddr{Addr: netip.MustParseAddrPort("10.1.0.4:41234")},
		RTT:    400 * time.Microsecond,
		HasRTT: true,
	}

	direct.BytesReceived, direct.HasBytesReceived = 8<<20, true

	said := pathNote([]iroh.PathInfo{direct})
	if !strings.Contains(said, "received 8 MiB") {
		t.Errorf("a path does not say what it carried inbound: %q", said)
	}

	if !strings.Contains(said, "10.1.0.4:41234") {
		t.Errorf("a direct path does not name where it went: %q", said)
	}

	if strings.Contains(said, "relay") {
		t.Errorf("a direct path was reported as relayed: %q", said)
	}

	url, err := netaddr.ParseRelayURL("https://relay.example/")
	if err != nil {
		t.Fatal(err)
	}

	relayed := iroh.PathInfo{
		Validated: true, HasAddr: true,
		Addr:   netaddr.RelayAddr{URL: url},
		RTT:    120 * time.Millisecond,
		HasRTT: true,
	}

	said = pathNote([]iroh.PathInfo{relayed})
	if !strings.Contains(said, "relay") {
		t.Errorf("a relayed path does not say so: %q", said)
	}

	// A path still being probed is not one carrying bytes.
	probing := direct
	probing.Validated = false

	if got := pathNote([]iroh.PathInfo{probing}); got != "" {
		t.Errorf("an unvalidated path was reported as carrying the transfer: %q", got)
	}

	if got := pathNote(nil); got != "" {
		t.Errorf("a connection with no paths said %q", got)
	}
}
