package cli_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// The linux sandbox does not claim the store is shared as root.
//
// The mirror of `TestTheDarwinSandboxSaysHowItShares`, and it matters for the
// opposite reason. There the shift is real and the guest cannot see it; here
// there is no VM and no share - the guest runs in a user namespace on the same
// filesystem, so `/proc/self/uid_map` is the truth and the guest already
// translates correctly (E495).
//
// A sandbox that claimed the shift anyway would apply it twice: the view would
// map the store's ownership to root *and* the guest would map what it sees, and
// Κ₂ would stop serving here to fix a problem it does not have.
//
// Not run on the machine this was written on. Said plainly rather than left for
// somebody to discover: it is compiled for linux and asserted there, and its
// darwin twin is the half that has been watched running.
func TestTheLinuxSandboxDoesNotShareAsRoot(t *testing.T) {
	t.Parallel()

	var sb exec.Sandbox = &exec.Native{}

	shared, ok := sb.(interface{ SharesStoreAsRoot() bool })
	if !ok {
		return // Saying nothing is saying no, which is what viewsFor reads.
	}

	if shared.SharesStoreAsRoot() {
		t.Error("the linux sandbox says the store is shared as root; there is" +
			" no share, and the guest's own uid_map already accounts for the" +
			" namespace")
	}
}
