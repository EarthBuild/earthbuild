package fleet

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// maxNode bounds a whole blob read into memory.
//
// A cache unit is a build-cache object or a package tarball; the largest
// measured in a real Go build cache was 12.7 MiB, and a helper module is about
// four. The bound is not about disk - it is that a peer answering a digest with
// a stream of arbitrary length must not be able to ask this process for
// unbounded memory.
const maxNode = 1 << 30

// Nearby is where to get a whole blob this machine does not hold.
//
// **The gap between a fleet that moves layers and one that moves a cache.**
// `Peers` is refreshed per assignment and serves *fragments* - parts of a layer,
// which is what faulting a base in needs. A cache's units, and the helper module
// that knows what a unit is, are whole blobs named by ℋ, and nothing held a live
// list of who to ask for one.
//
// Shaped after `Peers` on purpose: set by the runner where the holders are
// known, read by whatever runs during the step, and empty until an assignment
// says otherwise. A worker with nobody to ask behaves as every worker did
// before this - the cache does not cross, which is a slower build elsewhere.
type Nearby struct {
	mu   sync.RWMutex
	from []Source
}

// Set replaces who this worker can ask.
func (n *Nearby) Set(from []Source) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.from = from
}

// Node is the blob under this digest, from the nearest peer that has it.
//
// **Verified here, whatever the source said.** A `Source` returns verified
// streams and `PeerSource` catches a liar within a chunk, and neither is a
// reason to skip this: §5.3 makes what arrives from another trust domain
// unauthenticated data until this engine has checked it, and A5 is an assumption
// about that scepticism rather than about a peer's good faith.
//
// A source that errors, or answers wrongly, is skipped and the next asked - the
// position `sources` takes for a holder that will not dial, and for its reason:
// the address came from another machine's claim about itself. A wrong answer is
// a miss (I4), so a peer with total control of its own store can deny service
// and nothing else.
func (n *Nearby) Node(ctx context.Context, id ir.NodeID) ([]byte, error) {
	n.mu.RLock()
	from := n.from
	n.mu.RUnlock()

	for _, s := range from {
		got, err := s.Fetch(ctx, []ir.NodeID{id})
		if err != nil {
			continue
		}

		r, ok := got[id]
		if !ok {
			continue
		}

		b, err := io.ReadAll(io.LimitReader(r, maxNode))
		if err != nil || ir.DigestOf(b) != id {
			continue
		}

		return b, nil
	}

	return nil, fmt.Errorf("%w: no peer served %v", ErrNotFetched, id)
}
