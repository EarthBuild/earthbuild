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

	if root == "" || want == 0 {
		return Report{}, nil
	}

	free, err := Free(root)
	if err != nil {
		return Report{}, err
	}

	if free >= want {
		return Report{}, nil
	}

	// `SizeAll` rather than `Size`: the budgeted one gives up on a large store
	// and reports what it had reached, and a ceiling computed from an
	// undercount collects far more than the shortfall.
	size := SizeAll(root)

	// Sizing alone can spend the whole budget on a large store. Saying so beats
	// collecting against a ceiling derived from a walk that already ran long.
	if stop != nil && stop() {
		return Report{Stopped: true}, nil
	}

	keep := CeilingFor(size, want, free)

	return CollectUntil(root, keep, elsewhere, stop)
}
