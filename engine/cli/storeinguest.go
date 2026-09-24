package cli

import (
	"os"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

// storeInGuest reports whether this sandbox keeps its layers where the host
// cannot read them.
//
// **Asked of the sandbox, because it is a fact about the sandbox.** It was an
// OS default - true on darwin, false everywhere else - and that is wrong on
// Linux, where the microVM keeps its layers on a block device the host cannot
// open while the namespace backend on the same machine keeps them in a host
// directory. One platform, two answers, so the platform cannot be the one
// answering.
//
// What that cost: the host checked its own store for a layer, found it,
// concluded nothing needed sending, and then asked the guest to materialise a
// base it had never been given - `<layer> is in this step's base and this store
// holds neither a layer nor a declaration for it`. A microVM could not build
// `FROM alpine` on an empty store at all, and every corpus figure the microVM
// has ever produced was measured against a store that had been filled by
// something else.
//
// The setting still wins where it is set, because a sandbox that shares its
// store with the host - Apple's, with a shared mount - is efficient to read
// from here and there is no reason to ask the guest instead. See EnvStoreInVM.
func storeInGuest(sb exec.Sandbox) bool {
	switch os.Getenv(guest.EnvStoreInVM) {
	case "0", "false", "no":
		return false
	case "":
		return exec.StoreIsInGuest(sb)
	default:
		return true
	}
}
