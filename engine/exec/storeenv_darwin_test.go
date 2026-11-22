//go:build darwin

package exec

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// TestEveryExecNamesTheStoreTheGuestUses.
//
// **There are two stores inside the sandbox and only one of them is the
// store.** `/var/lib/earthbuild/store` is the host's directory over virtiofs;
// `/var/lib/earthbuild/fast/store` is the volume the guest owns, and with
// `EARTH_STORE_IN_VM` - the darwin default, and the correct one, because APFS
// is case-insensitive - that second one is where the layers are.
//
// The container is started against `guestRoot`, and the second execs that read
// the store named the shared mount instead. Counted on a live sandbox: 23,168
// entries on the mount against 103 in the volume, so the wrong answer is not
// even empty. It is a different build's layers, and a pack that found nothing
// reported `no layer … here` about a layer the store was holding (F4).
//
// This is why the fleet's pack failed at 1 GiB and not at 849 KiB: the small
// case had been built into both.
func TestEveryExecNamesTheStoreTheGuestUses(t *testing.T) {
	a := &Apple{}

	t.Setenv(guest.EnvStoreInVM, "1")

	if got := a.storeEnv(); !strings.HasSuffix(got, a.guestRoot()) {
		t.Errorf("an exec is given %q while the guest uses %q, so it reads a"+
			" different store from the one the build is using", got, a.guestRoot())
	}

	if !strings.Contains(a.storeEnv(), "fast") {
		t.Errorf("with the store on the guest's own device an exec was sent to"+
			" the shared mount: %q", a.storeEnv())
	}

	t.Setenv(guest.EnvStoreInVM, "0")

	if got := a.storeEnv(); !strings.HasSuffix(got, a.guestRoot()) {
		t.Errorf("an exec is given %q while the guest uses %q", got, a.guestRoot())
	}

	if strings.Contains(a.storeEnv(), "fast") {
		t.Errorf("with the store on the shared mount an exec was sent to the"+
			" guest's own device: %q", a.storeEnv())
	}
}
