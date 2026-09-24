package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/EarthBuild/earthbuild/engine/exec"

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
// ⚠ **A pruned store has not been observed returning to warm on the host.**
// Collecting this repository's own store down to 1GiB left every subsequent
// build at ~101s rather than the 0.65s it ran at before, publishing ~44 layers
// and ~48 fresh action-cache keys each time and matching none of them. Whether
// the collection causes that or reveals something already true of a cold chain
// is not settled (E574).
//
// **The guest's store does return.** `+earthly` under a microVM, pruned from
// 164 layers to 27 - 540.7 MiB freed, which is most of it - cost exactly one
// rebuild and then went back to what it was:
//
//	before   94 hit,  0 miss   1.20s
//	after-1   1 hit, 72 miss  15.68s
//	after-2  94 hit,  0 miss   1.21s   (and five more like it)
//
// So the collection does not by itself poison a chain, and E574 is about
// something the two paths do not share rather than about Collect. Not the same
// scale - 674 MiB against many gigabytes - so this narrows the question rather
// than closing it.
func Prune(o Options, keep uint64) error {
	// **Asked of the guest where the guest is the only one who can.** A
	// microVM's store is a fixed-size image the guest has mounted and this
	// process has never opened, so collecting the host's directory would tidy
	// something else and report success - leaving remaking the device as the
	// only way to reclaim the space, which is a purge where a prune was asked
	// for.
	sb, sbErr := sandbox("")
	if sbErr == nil && exec.StoreIsInGuest(sb) {
		return pruneInGuest(o, sb, keep)
	}

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

// pruneInGuest starts the sandbox and has it collect its own store.
func pruneInGuest(o Options, sb exec.Sandbox, keep uint64) error {
	e, err := exec.New(sb)
	if err != nil {
		return err
	}

	defer func() { _ = e.Close() }()

	said, err := e.PruneStore(context.Background(), keep)
	if err != nil {
		return err
	}

	if o.Out != nil {
		// **Not sb.StoreDir(), which is the host path this prune did not
		// touch.** Naming it would repeat, in the line announcing the fix, the
		// exact mistake the fix is for: a report about one store labelled with
		// another's location. The guest's own path is not knowable here either
		// - the sandbox reports whether the host can reach the store, not
		// where the guest keeps it - so this says only what is true.
		fmt.Fprintf(o.Out, "the store inside the sandbox: %s\n", said)
	}

	return nil
}
