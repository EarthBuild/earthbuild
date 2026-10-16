package exec

import "testing"

// storeInGuestSandbox keeps its layers where the host cannot write them.
type storeInGuestSandbox struct{ Sandbox }

func (storeInGuestSandbox) GuestStore() string { return "/store" }

// hostStoreSandbox keeps its layers on this machine.
type hostStoreSandbox struct{ Sandbox }

// Who unpacks an image is decided by where the store is, and that is a fact
// about the sandbox.
//
// **It asked the platform.** `guest.StoreInVM()` is true on darwin and false
// everywhere else, so on Linux the host unpacked an image into the host's own
// store - which a microVM, whose layers live on a block device the host cannot
// write, then could not read. `FROM alpine:3.24.1` on an empty guest store
// failed every time with:
//
//	<layer> is in this step's base and this store holds neither a layer nor a
//	declaration for it
//
// The comment two lines above the bug already said the rule: "a store on the
// guest's device implies the guest unpacks, because the host cannot write a
// block device it does not have". It was right; it just asked the wrong thing.
func TestWhoUnpacksFollowsWhereTheStoreIs(t *testing.T) {
	e := &Executor{sb: storeInGuestSandbox{}}
	if !e.unpacksInGuest() {
		t.Error("a sandbox whose store the host cannot write does not unpack in" +
			" the guest, so its images land where it cannot read them")
	}

	e = &Executor{sb: hostStoreSandbox{}}
	if e.unpacksInGuest() {
		t.Error("a sandbox sharing its store with the host unpacks in the guest," +
			" which sends bytes that were already readable")
	}

	// The setting still forces it, for a host that wants the guest to unpack
	// whatever the sandbox says.
	t.Setenv(EnvUnpackInGuest, "1")

	if !e.unpacksInGuest() {
		t.Errorf("%s no longer forces the guest to unpack", EnvUnpackInGuest)
	}
}
