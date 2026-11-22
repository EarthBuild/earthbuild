package store

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// sweepCacheBlobs removes the shared-cache blobs nothing points at.
//
// **A third population the collector had never seen.** `Collect` sweeps
// `layers/` and the `nodes/` a surviving manifest implies; a portable cache
// mount files its units and its maps in 𝔅 at the store root, sharded
// `<first two hex>/<digest>`. So a machine that shares caches grew for ever and
// `earth prune` reported freeing nothing.
//
// Reachability is the nodes argument one level longer. A pointer in
// `cachemaps/<id>/<scope>` names a map; the map names every unit. Anything else
// at the root is a map nothing points at any more - one per cache per build,
// which is what accumulates fastest - or a unit no map names.
//
// **A helper's module is swept with them, deliberately.** It is filed by the
// resolver at plan time on every build that names one, so losing it costs a
// re-read of a few megabytes here and a fetch from a peer there. Keeping it
// would need a root of its own, and a root that is never collected is the
// growth this function exists to stop.
//
// Best effort throughout: a pointer or a map that cannot be read keeps what it
// might have named, which is the same direction `sweepNodes` errs in. Losing a
// unit costs whatever the tool inside the cache does about it; keeping one costs
// disk, and only one of those is recoverable.
func sweepCacheBlobs(root string) (swept int, freed uint64) {
	live := reachableUnits(root)

	shards, err := os.ReadDir(root)
	if err != nil {
		return 0, 0
	}

	for _, shard := range shards {
		if !isShard(shard) {
			continue
		}

		at := filepath.Join(root, shard.Name())

		blobs, readErr := os.ReadDir(at)
		if readErr != nil {
			continue
		}

		for _, b := range blobs {
			id, parseErr := ir.ParseNodeID(b.Name())
			if parseErr != nil || live[id] {
				continue
			}

			size := uint64(0)
			if fi, statErr := b.Info(); statErr == nil && fi.Mode().IsRegular() {
				size = uint64(fi.Size()) //nolint:gosec // a file size is not negative
			}

			if os.Remove(filepath.Join(at, b.Name())) == nil {
				swept++
				freed += size
			}
		}

		// An emptied shard is two bytes of directory and will be remade the
		// moment something is filed under it. Ignored where it is not empty.
		_ = os.Remove(at)
	}

	return swept, freed
}

// isShard reports whether this entry is one of 𝔅's two-hex-character buckets.
//
// Unambiguous by construction: every other thing at the store root - `layers`,
// `nodes`, `mounts`, `actions`, `cachemaps`, `tmp` - is a word, and no word is
// two hexadecimal characters.
func isShard(e os.DirEntry) bool {
	if !e.IsDir() || len(e.Name()) != 2 {
		return false
	}

	return strings.IndexFunc(e.Name(), func(r rune) bool {
		return !strings.ContainsRune("0123456789abcdef", r)
	}) < 0
}

// reachableUnits is every blob a live cache pointer still implies.
//
// A pointer whose cache directory is gone is removed rather than followed: the
// directory is made when a step binds the mount, so its absence means the cache
// is not here - and a pointer nobody will follow again keeps a map and every
// unit in it alive for ever.
func reachableUnits(root string) map[ir.NodeID]bool {
	live := map[ir.NodeID]bool{}

	ids, err := os.ReadDir(filepath.Join(root, "cachemaps"))
	if err != nil {
		return live
	}

	for _, id := range ids {
		if !id.IsDir() {
			continue
		}

		scopes, scopeErr := os.ReadDir(filepath.Join(root, "cachemaps", id.Name()))
		if scopeErr != nil {
			continue
		}

		for _, scope := range scopes {
			at := filepath.Join(root, "cachemaps", id.Name(), scope.Name())

			if !cacheIsHere(root, id.Name(), scope.Name()) {
				_ = os.Remove(at)

				continue
			}

			followPointer(root, at, live)
		}
	}

	return live
}

// cacheIsHere reports whether the directory a pointer is about still exists.
func cacheIsHere(root, id, scope string) bool {
	fi, err := os.Stat(filepath.Join(root, "mounts", id, scope))

	return err == nil && fi.IsDir()
}

// followPointer marks a pointer's map and every unit it names as live.
func followPointer(root, at string, live map[ir.NodeID]bool) {
	b, err := os.ReadFile(at) //nolint:gosec // a path this engine wrote
	if err != nil {
		return
	}

	mapID, err := ir.ParseNodeID(strings.TrimSpace(string(b)))
	if err != nil {
		return
	}

	live[mapID] = true

	h := mapID.String()

	body, err := os.ReadFile(filepath.Join(root, h[:2], h)) //nolint:gosec // a path built from a digest
	if err != nil {
		// A map this store no longer holds names nothing this can protect, and
		// the pointer stays: the next share re-files a map under it.
		return
	}

	for _, line := range strings.Split(string(body), "\n") {
		_, digest, found := strings.Cut(line, "\t")
		if !found {
			continue
		}

		if id, parseErr := ir.ParseNodeID(strings.TrimSpace(digest)); parseErr == nil {
			live[id] = true
		}
	}
}
