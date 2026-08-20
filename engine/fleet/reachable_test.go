package fleet

import (
	"testing"

	"github.com/tmc/go-iroh/iroh"
	"github.com/tmc/go-iroh/key"
)

// A fleet endpoint can be reached from another machine.
//
// go-iroh binds **direct-only** by default: `WithRelayMode`'s own doc says the
// default is `relay.ModeDisabled`, and nothing configures endpoint discovery. So
// a peer holding only a node id had no way to resolve it -
// `iroh: no reachable address for endpoint` - and a fleet could only form
// between machines that could already dial each other by address (E505).
//
// That is not a property of the design. Rust iroh, which rebuck2 uses to form a
// throwaway cluster out of three GitHub runners, has relays and DNS discovery on
// by default. The Go binding does not, and nothing here turned them on.
//
// Only where a fleet was asked for: these options are applied when a driver
// binds or a worker joins, and an ordinary local build binds no endpoint at all.
func TestAFleetEndpointIsReachableFromElsewhere(t *testing.T) { //nolint:paralleltest // t.Setenv
	t.Setenv(EnvDiscover, "1")

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}

	opts := endpointOptions(sk)
	if len(opts) == 0 {
		t.Fatal("a fleet endpoint is bound direct-only, so a peer that has" +
			" only a node id cannot reach it")
	}

	// Two: a relay mode and an address lookup. Counted rather than inspected -
	// an `iroh.Option` is a closure over that package's unexported config and
	// there is no way to ask it what it did from here.
	//
	// **What this cannot prove is the thing that matters**: that a peer on
	// another machine resolves this endpoint. That needs two machines and the
	// network, and the place it gets proved is a workflow with a driver job and
	// two worker jobs - which is what rebuck2 does and what this unblocks.
	// Said here rather than left as a green tick meaning more than it does.
	if len(opts) != 2 {
		t.Errorf("%d option(s), want a relay mode and an address lookup", len(opts))
	}

	// They are at least valid: an option that errors on apply would fail every
	// bind, and this is the cheapest check that they are the real ones.
	if _, err := iroh.Bind(t.Context(), opts...); err != nil {
		t.Errorf("binding with them failed: %v", err)
	}
}

// And it is off unless asked for.
//
// A fleet on one LAN needs neither, reaching a third party's infrastructure to
// find one's own workers should be a choice, and - the reason it is opt-in
// rather than opt-out - turning it on regressed the path that was working
// (E505).
func TestFleetDiscoveryIsOffUnlessAskedFor(t *testing.T) {
	t.Setenv(EnvDiscover, "")

	sk, err := key.GenerateSecretKey()
	if err != nil {
		t.Fatal(err)
	}

	if got := endpointOptions(sk); len(got) != 0 {
		t.Errorf("%s is unset and %d option(s) were applied anyway", EnvDiscover, len(got))
	}
}
