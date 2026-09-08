package store

import (
	"os"
	"path/filepath"
	"regexp"
)

// partialName matches the directory a half-written layer leaves behind.
//
// Written by the guest as `os.MkdirTemp(dir, "."+id+".partial-")`, so the name
// is a dot, the layer's id, `.partial-` and whatever MkdirTemp appended. Matched
// exactly rather than by prefix: this removes directories, and the store may
// hold names belonging to something else entirely - `candidates` leaves those
// alone for the same reason and says so.
var partialName = regexp.MustCompile(`^\.[0-9a-f]{64}\.partial-[0-9]+$`)

// sweepPartials removes the debris of layer writes that did not finish, and
// says how much it freed.
//
// **Nothing else ever removed these.** A layer is staged in
// `.<id>.partial-<n>` and renamed into place when it is whole; the writer
// deletes it on an error, but a *killed* writer deletes nothing. `candidates`
// then skips the name because it does not parse as a layer id - correct for a
// stranger's file, wrong for this engine's own leavings - so the bytes were
// lost for good and were not even counted in the store's size. A collector can
// therefore decide a store fits while its disk is full of them.
//
// Not hypothetical: one session of killed guests took a 195G store to 7M free,
// and a collection asked for 20G could not find it.
//
// Safe here because collection assumes no build is using the store: the agent
// collects at startup before any step runs, a device-backed store is claimed
// exclusively, and `Prune` documents the same assumption. Without it a sweep
// could take a write that is still happening.
func sweepPartials(dir string) (int, uint64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}

	var (
		swept int
		freed uint64
	)

	for _, e := range entries {
		if !e.IsDir() || !partialName.MatchString(e.Name()) {
			continue
		}

		at := filepath.Join(dir, e.Name())

		// Sized before removal, because afterwards there is nothing to ask.
		size := SizeAll(at)

		if os.RemoveAll(at) != nil {
			// Left for the next collection rather than reported as freed: a
			// figure that counts what is still there is worse than a small one.
			continue
		}

		swept++
		freed += size
	}

	return swept, freed
}
