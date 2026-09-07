package guest_test

import (
	"net"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// TestAHostThatCannotFaultAPathInSaysSoRatherThanCrashing.
//
// **The relay outlived the reason it was started.** It used to run only where
// something could fault a path in, so `fill` was never nil inside it and the
// `default` arm could call it without asking. Streaming a blob to the guest
// then became a second reason to start the relay - and on macOS the default
// one - so it now runs on builds that have no filler at all, and the first
// fault-in request dereferences nothing and takes the process with it.
//
// `progress` has been guarded against exactly this since it was added; `fill`
// was not, because until the relay had two reasons to exist it could not
// happen. A segfault is also the worst possible way to say it: no path, no
// handle, and a stack in the guest package for a decision made in the host's.
func TestAHostThatCannotFaultAPathInSaysSoRatherThanCrashing(t *testing.T) {
	t.Parallel()

	here, there := net.Pipe()

	go func() {
		_ = guest.ServeFillsAnd(there, nil,
			func(_ string, have int64) (int64, error) { return have + 1, nil })
	}()

	err := guest.NewFills(here).Fill("/etc/hosts")
	if err == nil {
		t.Fatal("a host with no filler accepted a fault-in request" +
			"\n  there is nothing behind it, so the answer cannot be yes")
	}

	// The path, or the answer is indistinguishable from every other refusal
	// and the one thing worth knowing is which path went unanswered.
	if !strings.Contains(err.Error(), "/etc/hosts") {
		t.Errorf("the refusal does not name the path:\n  %v", err)
	}
}
