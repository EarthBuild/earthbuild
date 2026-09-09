package store

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// collectUntilFree removes layers, least recently used first, until the
// filesystem reports the free space asked for.
//
// **Asking the filesystem is 2.2us; measuring the store is 5.1s.** The older
// path derived a byte ceiling from `SizeAll`, which walks every file in the
// store, and then walked again inside `candidates` to size each layer - on a
// store of 696,191 files that is five seconds spent measuring before a single
// byte is freed. Against a five-second budget, collection was almost entirely
// measurement, which is why it could not keep pace with the builds filling the
// store.
//
// Free space is also the thing actually wanted, and it is exact. A ceiling
// derived from a size estimate is neither, and it retires the E574 class of
// bug outright: there is no estimate left to be wrong, and no floor to be
// mistaken for a total.
//
// The reading is of the *filesystem*, not of the store. For a guest's store
// that is a device of its own, so the two are the same thing. Where the store
// shares a filesystem they are not, and that difference is dangerous rather
// than merely imprecise: if the disk is full of somebody else's data, no
// number of layers removed will reach the target, and a collector that keeps
// going until it does empties the entire cache and still fails. See futile.
//
// free is a parameter so the stop condition can be tested without a disk.
func collectUntilFree(
	root string,
	want uint64,
	elsewhere func(ir.NodeID) bool,
	stop func() bool,
	free func(string) (uint64, error),
) (Report, error) {
	report := Report{}

	// Debris first and unconditionally: nothing can use a half-written layer,
	// so there is no reading of "the store has room" that makes keeping one
	// right. See sweepPartials.
	report.Debris, _ = sweepPartials(root)

	began, err := free(root)
	if err != nil {
		return report, err
	}

	if began >= want {
		return report, nil
	}

	index, err := OpenIndex(root)
	if err != nil {
		return report, err
	}

	// Named, never sized. Ordering is by last use and by identity, neither of
	// which costs a walk - the whole point of this path.
	names, err := layerNames(root)
	if err != nil {
		return report, err
	}

	recoverable := func(id ir.NodeID) bool { return elsewhere != nil && elsewhere(id) }

	sort.Slice(names, func(i, j int) bool {
		iAway, jAway := recoverable(names[i]), recoverable(names[j])
		if iAway != jAway {
			return iAway
		}

		iUsed, jUsed := index.Used(names[i]), index.Used(names[j])
		if iUsed.Equal(jUsed) {
			return names[i].String() < names[j].String()
		}

		return iUsed.Before(jUsed)
	})

	report.Kept = len(names)
	now := began

	// futile counts removals that have not moved the filesystem's figure since
	// the collection began.
	//
	// **The bound that stops a shared disk taking the whole cache.** Free space
	// is only the store's to reclaim where the store is what filled it; when
	// something else has, every removal is a layer lost for nothing. Deleting
	// is not helping, so it stops.
	//
	// Counted against the reading at the start rather than the previous one,
	// so a single removal that happens to free nothing - an empty layer, or an
	// allocator that has not caught up - does not end a collection that is
	// working.
	futile, limit := 0, futileLimit(len(names))

	for _, id := range names {
		if stop != nil && stop() {
			report.Stopped = true

			break
		}

		// Forget before deleting, which is Index's own ordering: interrupted
		// here, the index lags and describes a store holding more than it
		// claims - the harmless direction.
		_ = index.Forget(id)

		err := os.RemoveAll(LayerStore(root).Path(id))
		if err != nil {
			return report, err
		}

		report.Removed++
		report.Kept--

		now, err = free(root)
		if err != nil {
			return report, err
		}

		if now >= want {
			break
		}

		if now > began {
			futile = 0
		} else {
			futile++

			if futile >= limit {
				report.Stopped = true

				break
			}
		}
	}

	report.Reclaimed = now - began

	return report, nil
}

// futileLimit is how many removals may free nothing before collection gives up.
//
// Proportional, because a fixed number is wrong at both ends: sixteen is a
// handful of a large store and the whole of a small one, and the point is to
// lose a fraction rather than everything. A quarter, capped at sixteen, and
// never less than one.
//
// Large enough that a run of tiny layers, or a filesystem slow to report, does
// not end a collection that is working; small enough that a disk somebody else
// filled costs a few layers rather than the cache.
func futileLimit(layers int) int {
	const (
		cap   = 16
		share = 4
	)

	n := layers / share
	if n > cap {
		n = cap
	}

	if n < 1 {
		n = 1
	}

	return n
}

// layerNames is every layer id the store holds, without measuring any of them.
func layerNames(root string) ([]ir.NodeID, error) {
	entries, err := os.ReadDir(filepath.Join(root, "layers"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	names := make([]ir.NodeID, 0, len(entries))

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		id, err := ir.ParseNodeID(e.Name())
		if err != nil {
			continue
		}

		names = append(names, id)
	}

	return names, nil
}
