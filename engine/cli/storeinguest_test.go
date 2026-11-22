package cli

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

// guestStored is a sandbox whose layers live where the host cannot read them.
type guestStored struct{ exec.Sandbox }

func (guestStored) GuestStore() string { return "/store" }

// hostStored is a sandbox that keeps its layers on this machine.
type hostStored struct{ exec.Sandbox }

// Where the store lives is a fact about the sandbox, not about the platform.
//
// **It was an OS default: true on darwin, false everywhere else.** A microVM on
// Linux keeps its layers on a block device the host cannot open, so the host
// checked its own store for a layer, found it, concluded nothing needed
// sending, and then asked the guest to materialise a base it had never been
// given:
//
//	<layer> is in this step's base and this store holds neither a layer nor a
//	declaration for it
//
// The same default is right for the namespace backend on the same machine,
// whose store *is* a host directory - which is why this cannot be answered by
// the platform and has to be asked of the sandbox.
func TestWhereTheStoreLivesIsAskedOfTheSandbox(t *testing.T) {
	for _, c := range []struct {
		name string
		env  string
		sb   exec.Sandbox
		want bool
	}{
		{"a guest-stored sandbox, nothing said", "", guestStored{}, true},
		{"a host-stored sandbox, nothing said", "", hostStored{}, false},
		{"a guest-stored sandbox, switched off", "0", guestStored{}, false},
		{"a host-stored sandbox, switched on", "1", hostStored{}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(guest.EnvStoreInVM, c.env)

			if got := storeInGuest(c.sb); got != c.want {
				t.Errorf("storeInGuest = %v, want %v", got, c.want)
			}
		})
	}
}

var _ = context.Background
