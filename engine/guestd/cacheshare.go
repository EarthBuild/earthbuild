package guestd

import (
	"os"

	"github.com/EarthBuild/earthbuild/engine/cacheshare"
	"github.com/EarthBuild/earthbuild/engine/guest"
)

// sharesCaches is what fills and offers a cache mount inside this guest.
//
// **The wasm runtime lives here because the cache does.** On a microVM the store
// is a device nothing outside has mounted, so the helper that knows what a unit
// is has to run on this side of the boundary - which is the same argument that
// put layer assembly and collection here (E1b, KindPrune).
//
// No build directory: a guest has no Earthfile, so an unpinned `--helper
// ./go.wasm` names a file on the machine that read the Earthfile and nothing
// here. A helper arrives pinned or the cache does not cross, which is a slower
// build somewhere else and never a wrong one.
//
// Complaints go to stderr, which is the guest's console: a cache that did not
// cross is a slower build and a cache that silently did not cross is a fleet
// nobody can explain (I11).
func sharesCaches(layerDir string) guest.CacheSharing {
	return cacheshare.New(layerDir, "", os.Stderr)
}
