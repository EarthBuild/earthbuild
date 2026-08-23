package cli

import (
	"fmt"
	"io"

	"github.com/EarthBuild/earthbuild/engine/store"
)

// Prune removes layers, least recently used first, until the store fits.
//
// **Asked for, never automatic.** The store is a cache and a cache that empties
// itself on somebody else's schedule is a build that is slow for reasons nobody
// can see. This deletes what a build would otherwise reuse, so it happens when a
// person says so and prints what it did.
//
// Safe to run against a store a build will use next: a collected layer is a miss
// and a rebuild, not a failure (E573). It is not safe to run *while* a build is
// using that store - nothing yet stops two processes sharing a directory, which
// is the one-writer question phase 3 answers with a device (E571).
//
// ⚠ **A pruned store has not been observed returning to warm.** Collecting this
// repository's own store down to 1GiB left every subsequent build at ~101s
// rather than the 0.65s it ran at before, publishing ~44 layers and ~48 fresh
// action-cache keys each time and matching none of them. Whether the collection
// causes that or reveals something already true of a cold chain is not settled
// (E574). Until it is, this reclaims space reliably and does not reliably leave
// a cache behind.
func Prune(o Options, keep uint64) error {
	dir, err := storeDir()
	if err != nil {
		return err
	}

	report, err := store.Collect(dir, keep)
	if err != nil {
		return err
	}

	if o.Out != nil {
		say(o.Out, dir, report)
	}

	return nil
}

func say(w io.Writer, dir string, r store.Report) {
	fmt.Fprintf(w, "%s\n  %s\n", r, dir)

	if r.Removed == 0 && r.Before > 0 {
		fmt.Fprintf(w, "  already within the ceiling; nothing to do\n")
	}
}
