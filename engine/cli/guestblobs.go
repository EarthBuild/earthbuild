package cli

import (
	"errors"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// errAskFailed stands for a store that could not be reached. Named so a test
// can produce one without inventing a transport failure.
var errAskFailed = errors.New("the store could not be asked")

// errNoPathsGiven reports a view asked for without the paths it would need to
// answer in one question. See guestViews.View.
var errNoPathsGiven = errors.New("a view of a store held elsewhere needs the paths it will be asked about")

// guestBlobs answers "is this layer present" by asking whoever holds the store.
//
// **A store on the guest's device is not on the host's filesystem.** `Lookup`
// refuses an entry whose layer the blob store cannot find - "a claim whose
// result is not present is not usable, however well signed" - so a host that
// stats its own root reads an empty answer and rebuilds everything it already
// had. `KindStoreHas` was written for this, one question before it happened.
//
// Presence is remembered and absence is not. A layer the store holds is
// immutable and stays held for this build, so one question answers every later
// lookup; a layer it does not hold yet is very often one this build is about to
// place, and remembering "no" would deny every lookup after it arrives.
type guestBlobs struct {
	ask func(ids []ir.NodeID) ([]ir.NodeID, error)

	// Why reports the first question that could not be asked at all.
	//
	// **Because the symptom is silence.** A failed question is a miss, a miss
	// is "do the work", and a build that does the work is correct - so a store
	// that cannot be reached costs every hit this build had and says nothing.
	// It reads as a cache that does not work rather than as a store that cannot
	// be asked, and those want different fixes.
	Why func(error)

	// askContent is what each of these layers holds, times excluded, one entry
	// per id and in that order. Nil where nobody can answer, which leaves Κₜ
	// not derivable and the build on the key it already had.
	askContent func(ids []ir.NodeID) ([]ir.NodeID, error)

	mu   sync.Mutex
	seen map[ir.NodeID]bool
	held map[ir.NodeID]ir.NodeID
	said bool
}

// sayOnce reports the first failure and no others: one unreachable store
// produces one failed question per lookup, and a build has thousands.
func (b *guestBlobs) sayOnce(err error) {
	b.mu.Lock()
	first := !b.said
	b.said = true
	b.mu.Unlock()

	if first && b.Why != nil {
		b.Why(err)
	}
}

// Has reports whether the store holds a layer.
//
// A question that cannot be asked is a miss, which means "do the work" and is
// always correct. Answering present on a failed question would be a hit on a
// result that may not exist, which is the one thing Λ may never do (I4).
func (b *guestBlobs) Has(id ir.NodeID) bool {
	b.mu.Lock()
	if b.seen[id] {
		b.mu.Unlock()

		return true
	}
	b.mu.Unlock()

	held, err := b.ask([]ir.NodeID{id})
	if err != nil {
		b.sayOnce(err)

		return false
	}

	if len(held) == 0 {
		return false
	}

	b.mu.Lock()
	if b.seen == nil {
		b.seen = map[ir.NodeID]bool{}
	}

	for _, h := range held {
		b.seen[h] = true
	}
	b.mu.Unlock()

	return true
}

// ContentOf is a layer's identity with times excluded, asked of whoever holds
// the store.
//
// **Because the manifest that answers is not on this filesystem.** Κₜ (green
// paper 4.5a) names a base by what its layers hold, and the fold that produces
// one reads the manifest beside the layer - which on a disk the guest owns the
// host cannot see. A host that folds it itself reads nothing, derives no key,
// and the tier written for rebuilt bases does nothing on exactly the builds
// with the most to gain.
//
// Remembered, because a base is consulted once per step and a build has many;
// a round trip per lookup is the cost KindStoreHas was batched to avoid and
// this would reintroduce one layer at a time.
//
// A question that cannot be asked is no content, which leaves the key
// underivable and the build on Κ₁ - the answer it had before this existed.
// Answering with something plausible would be worse than answering nothing: two
// bases holding anything at all would share a key.
func (b *guestBlobs) ContentOf(id ir.NodeID) (ir.NodeID, bool) {
	if b.askContent == nil {
		return ir.NodeID{}, false
	}

	b.mu.Lock()
	known, seen := b.held[id]
	b.mu.Unlock()

	if seen {
		return known, known != ir.NodeID{}
	}

	got, err := b.askContent([]ir.NodeID{id})
	if err != nil {
		b.sayOnce(err)

		return ir.NodeID{}, false
	}

	if len(got) != 1 {
		return ir.NodeID{}, false
	}

	// Remembered either way. A layer the store cannot describe will not become
	// describable, and asking again for every step above it is the round trip
	// this cache exists to spend once.
	b.mu.Lock()
	if b.held == nil {
		b.held = map[ir.NodeID]ir.NodeID{}
	}

	b.held[id] = got[0]
	b.mu.Unlock()

	return got[0], got[0] != ir.NodeID{}
}
