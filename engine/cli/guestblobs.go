package cli

import (
	"errors"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// errAskFailed stands for a store that could not be reached. Named so a test
// can produce one without inventing a transport failure.
var errAskFailed = errors.New("the store could not be asked")

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

	mu   sync.Mutex
	seen map[ir.NodeID]bool
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
	if err != nil || len(held) == 0 {
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
