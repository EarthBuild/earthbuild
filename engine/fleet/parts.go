package fleet

import (
	"context"
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Parts is what a worker serves: whole layers where it has them, and parts of
// layers where it has only those.
//
// **A worker that has just fetched exactly the bytes the next machine needs
// should be the one to send them.** Without this, fragments come only from
// whoever holds the whole layer - the driver - so adding machines adds queueing
// at one uplink rather than throughput, which is E260 on the path that since
// E323 is the one that wins.
//
// Either half may be nil. A fleet sharing one store has no fragments; a worker
// that has never been given a layer store has no whole ones.
type Parts struct {
	Whole *Layers
	Some  *Fragments
}

// Has answers about the **whole** layer, and only that.
//
// The distinction is load-bearing: `Has` is what the blob server checks before
// sending a layer, and a worker holding one file of a base must not claim it.
// The fragment path asks its own question (see Fragment), which is what the
// server used to conflate with this one (E325).
func (p *Parts) Has(id ir.NodeID) bool { return p.Whole != nil && p.Whole.Has(id) }

// Get is the whole layer, if this worker has the whole layer.
func (p *Parts) Get(id ir.NodeID) ([]byte, error) {
	if p.Whole == nil {
		return nil, fmt.Errorf("%w: no layer %v here", ErrNotFetched, id)
	}

	return p.Whole.Get(id) //nolint:wrapcheck // the store's own error
}

// Fragment sends part of a layer from wherever this worker has it.
//
// The whole layer first: a store that has everything can cut any subset, while
// a fragment store can answer only for the exact set it was given. Both are
// tried, because a worker commonly has some bases whole and others in parts.
func (p *Parts) Fragment(
	id ir.NodeID, want []string,
) (manifest, packed []byte, err error) {
	if p.Whole != nil && p.Whole.Has(id) {
		return p.Whole.Fragment(id, want)
	}

	if p.Some != nil {
		// Always with the proof: `serveOneBlob` drops it when the caller says it
		// already has one, and that decision belongs there rather than in every
		// store that can answer.
		return p.Some.Fragment(context.Background(), id, want, true)
	}

	return nil, nil, fmt.Errorf("%w: no fragment of %v here", ErrNotFetched, id)
}
