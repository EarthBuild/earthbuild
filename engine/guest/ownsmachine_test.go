package guest_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// A guest stops the machine it was told it owns, and no other.
//
// The grant is the safety property. A backend that starts a VM for one guest
// holds it open with a keep-alive at PID 1, and the guest going idle means the
// machine has nothing to do; a backend that confines with namespaces has the
// *host's* init at PID 1, and signalling that is a considerably worse bug than
// the leaked VM this fixes (E555).
//
// So the right is granted rather than discovered: the guest cannot tell from
// inside which of those it is in.
func TestAGuestOnlyStopsAMachineItWasGranted(t *testing.T) {
	t.Setenv(guest.EnvOwnsMachine, "")

	if guest.OwnsMachine() {
		t.Error("a guest with no grant believes it may stop its machine," +
			"\n  which on a namespace backend is the host's init")
	}

	// Nothing is signalled without the grant, on any platform. Called rather
	// than merely asserted about, because the whole risk lives in the call.
	err := guest.StopMachine()
	if err != nil {
		t.Errorf("stopping a machine this guest does not own failed"+
			" instead of doing nothing: %v", err)
	}

	t.Setenv(guest.EnvOwnsMachine, "1")

	if !guest.OwnsMachine() {
		t.Error("a guest that was granted its machine does not believe it," +
			"\n  so an idle sandbox stops its agent and leaves the VM running")
	}
}
