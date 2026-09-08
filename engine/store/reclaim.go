package store

import (
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// CeilingFor is the size a store must come down to so that `want` bytes are
// free, given what it holds now and what is free now.
//
// **Exactly the shortfall, and not a byte more.** Collecting to some fraction
// of the disk is easier to write and throws away layers nobody asked it to:
// every byte past the shortfall is a rebuild somebody pays for later. A store
// with the room already returns zero, which means nothing to do.
//
// Unsigned, so the underflow matters: a shortfall larger than the store would
// wrap to an enormous ceiling, which reads as "keep everything" and reclaims
// nothing at all - the failure being silent, and arriving exactly when the store
// is most desperate.
func CeilingFor(size, want, free uint64) uint64 {
	if free >= want {
		return 0
	}

	short := want - free
	if short >= size {
		return 0
	}

	return size - short
}

// Reclaim brings a store down to leave `want` bytes free, and says what it did.
//
// **Called where nothing else is running.** There is no lock: a build that read
// a layer this removed would materialise a filesystem missing an element, so the
// only safe moment is before the builds start - which is a guest coming up.
//
// Nothing to do is the common case and costs one statfs. A store whose room
// cannot be measured is left alone: collecting on a guess throws a cache away
// for a number nobody has.
func Reclaim(root string, want uint64, elsewhere func(ir.NodeID) bool) (Report, error) {
	return ReclaimWithin(root, want, elsewhere, 0)
}

// ReclaimWithin is Reclaim, giving up after within.
//
// Zero means no limit, which is what `earth prune` wants: somebody asked for
// the space and should get it. A budget is for the collection nobody asked for
// - the one the agent runs at startup, where the cost is paid by a host waiting
// on a handshake. See CollectUntil.
//
// The clock starts here rather than at the loop, because walking the store to
// size it is itself most of the cost on a large one: a budget that only bounded
// the deleting would be spent before it was consulted.
func ReclaimWithin(
	root string, want uint64, elsewhere func(ir.NodeID) bool, within time.Duration,
) (Report, error) {
	began := time.Now()

	var stop func() bool
	if within > 0 {
		stop = func() bool { return time.Since(began) > within }
	}

	if root == "" {
		return Report{}, nil
	}

	// **Debris first, and unconditionally.** A half-written layer is not a
	// cache entry being kept against future use - nothing can ever use one -
	// so there is no version of "the store has room" that makes keeping it
	// right. Swept before the free space is even measured, because every one
	// still on disk makes that measurement less true.
	//
	// This used to sit after the early return below, so debris was cleared only
	// once the store was already short. It therefore accumulated through all
	// the healthy time and was first noticed when there was a great deal of it
	// and a full store to dig out of.
	debris, freed := sweepPartials(root)

	swept := Report{Debris: debris, Before: freed}

	// `want == 0` means "do not collect", which is about layers: they are worth
	// keeping until the space is wanted. It has never meant "keep the remains
	// of writes that were killed", and a store told not to collect is exactly
	// the one where those would otherwise pile up untouched for ever.
	if want == 0 {
		return swept, nil
	}

	free, err := Free(root)
	if err != nil {
		return swept, err
	}

	if free >= want {
		return swept, nil
	}

	// **The budget is reconsidered now that the shortfall is known.** A store
	// with almost nothing left is not being tidied, it is being rescued, and
	// stopping early there is how a sweep kept failing on space with the
	// collector reporting it had given up every time.
	if budgetFor(within, want, free) == 0 {
		stop = nil
	}

	// `SizeAll` rather than `Size`: the budgeted one gives up on a large store
	// and reports what it had reached, and a ceiling computed from an
	// undercount collects far more than the shortfall.
	size := SizeAll(root)

	// Sizing alone can spend the whole budget on a large store. Saying so beats
	// collecting against a ceiling derived from a walk that already ran long.
	if stop != nil && stop() {
		swept.Stopped = true

		return swept, nil
	}

	keep := CeilingFor(size, want, free)

	report, err := CollectUntil(root, keep, elsewhere, stop)

	// The sweep above already happened, and CollectUntil's own sweep found
	// nothing left to do - so its counts are added rather than replaced.
	report.Debris += swept.Debris
	report.Before += swept.Before

	return report, err
}

// rescueFloor is the free space below which collection stops being optional.
//
// A tenth of what was asked for. Above it the store is merely untidy and a
// budget is the right trade; below it the next capture is what fails, and a
// build that waits is strictly better than a build that dies part-way through
// writing a layer.
const rescueFloor = 10

// budgetFor is how long collection may take, given what the store has.
//
// **Budget the housekeeping, not the rescue.** The budget exists so routine
// tidying never makes a guest look unresponsive, and it was applied equally to
// a store with a little less room than it wanted and one with almost none.
// Those are different situations: the first is a cache that will be tidied
// eventually, the second is a build about to fail with "no space left on
// device".
//
// Spending longer became cheap when collection moved off the handshake path: a
// long collection now delays the first request instead of killing the
// connection.
//
// Zero means no limit and is returned unchanged - an operator who asked for a
// full collection gets one whatever the store looks like.
func budgetFor(budget time.Duration, want, free uint64) time.Duration {
	if budget == 0 || free < want/rescueFloor {
		return 0
	}

	return budget
}
