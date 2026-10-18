package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// **This backend's virtual NIC forwards one MAC and drops the rest.**
//
// A step's own network namespace is built by hanging a macvlan off the guest's
// interface, which gives the child its own MAC - and
// Virtualization.framework's NIC silently discards its frames. The step comes up
// with the right address and the right default route and cannot reach its own
// gateway: not a routing failure but a layer-2 one, which reads as neither.
//
// ipvlan would share the parent's MAC and is the usual answer, and this VM's
// kernel refuses it with EOPNOTSUPP - it is not built in.
//
// So the backend says what it knows. It already tells the guest four other
// things about itself; this is a fifth, and the operator can still override it.
func TestThisBackendDefaultsStepsToASharedNetwork(t *testing.T) {
	t.Parallel()

	if got := stepNetSetting(""); got != guest.NetShared {
		t.Errorf("with nothing set the guest is told %q, want %q", got, guest.NetShared)
	}

	// An operator who asks for isolation gets it, and keeps the consequence.
	if got := stepNetSetting(guest.NetPrivate); got != guest.NetPrivate {
		t.Errorf("with private asked for the guest is told %q", got)
	}

	if got := stepNetSetting(guest.NetShared); got != guest.NetShared {
		t.Errorf("with shared asked for the guest is told %q", got)
	}
}
