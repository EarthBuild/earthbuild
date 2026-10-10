package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/blob"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

// maxHelper bounds what will be read as a helper module.
//
// A helper is a wasm module and the ones this repository builds are about four
// megabytes. The bound is not about disk: a path fat-fingered onto a multi-
// gigabyte artefact would otherwise be read into memory and hashed before
// anything noticed it was not a module.
const maxHelper = 64 << 20

// helperResolver pins a cache helper to the digest of its module.
//
// **Θ's argument, one construct over** (I17). A helper decides what a unit is,
// what it is called and what bytes go in each frame, so `Helper` is in Κ₁ on the
// grounds that two ends must agree about it - but what was hashed was the path
// the author typed, and two machines can hold one path over different bytes.
// The agreement being enforced was an agreement about spelling.
//
// **Filing the module is the point, not a side effect.** A pinned image
// reference is a name a registry will answer for; a pinned helper is a name
// nobody can answer for until the bytes are somewhere both ends read. 𝔅 is that
// place and is already the one the cache's own units go to, so a worker fetches
// a helper by exactly the route it fetches everything else - digest-named,
// verified on read, unpoisonable (§2.1).
//
// A reference that cannot be read leaves the mount unpinned rather than failing
// the build, which is what `imageResolver` does for an unreachable registry and
// for the same reason: a cache that does not cross is a slower build somewhere
// else, and a refused step is no build at all.
func (g *engine) helperResolver(storeDir string) interp.ResolveHelper {
	// **No store, no pin.** `storeDir` failing is a machine that cannot keep
	// blobs at all, and a digest naming bytes nowhere is worse than no digest:
	// it keys the step as pinned and leaves the far end unable to fetch what it
	// names.
	if storeDir == "" {
		return nil
	}

	var sink *blob.Store

	return func(ref, dir string) (string, error) {
		at := ref
		if !filepath.IsAbs(at) {
			// The Earthfile's own directory, which the interpreter supplies:
			// a relative path in an Earthfile means that Earthfile's directory,
			// wherever the build happened to be started from.
			at = filepath.Join(dir, at)
		}

		fi, err := os.Stat(at)
		if err != nil {
			return "", fmt.Errorf("read the helper %s: %w", ref, err)
		}

		if fi.Size() > maxHelper {
			return "", fmt.Errorf("the helper %s is %d bytes, and this reads at"+
				" most %d - is that path a wasm module?", ref, fi.Size(), maxHelper)
		}

		module, err := os.ReadFile(at) //nolint:gosec // a path the Earthfile named
		if err != nil {
			return "", fmt.Errorf("read the helper %s: %w", ref, err)
		}

		if sink == nil {
			if sink, err = blob.New(storeDir); err != nil {
				return "", fmt.Errorf("open the blob store: %w", err)
			}
		}

		id, _, err := sink.Put(bytes.NewReader(module))
		if err != nil {
			return "", fmt.Errorf("file the helper %s: %w", ref, err)
		}

		return id.String(), nil
	}
}
