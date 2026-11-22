package store

import (
	"os"
	"runtime"
	"slices"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// held is one chain's fold, kept so the chain above it need not repeat it.
type held struct {
	stack []ir.NodeID
	fold  *layer.Fold
	read  int // manifests decoded into fold; a stack of none is not a tree
}

// Folder answers TreeOf, reusing the folds it did last time where it can.
//
// **A build asks about ladders, and it asks about several at once.** Κₜ consults
// a step's base, and step 𝑖 of a target has step 𝑖-1's stack with one layer on
// top - so consecutive questions down one chain share every layer but the last.
// Holding that fold turns "every step pays for the whole base" into "the base is
// folded once per chain".
//
// One fold per chain rather than one in total, because the scheduler claims the
// cache *before* it takes a parallelism slot: derivations from independent
// branches interleave, so a single held prefix is thrown away by its neighbour
// before it is ever extended. Measured on a 20k-entry base, twelve steps: one
// chain cost 9.3ms a fold and two chains cost 15.7ms, which is what folding from
// scratch cost - the entire saving, gone at a parallelism of two.
//
// A chain that matches nothing held evicts the least recently used, which costs
// exactly what every stack cost before this existed. The bound is memory: a fold
// holds its merged set, so slots are capped rather than grown per branch.
type Folder struct {
	dir   string
	slots int

	mu   sync.Mutex
	held []*held // most recently used first
}

// NewFolder holds no stack yet.
//
// Sized by the machine, because the scheduler's width is the number of chains
// that can interleave and the host does not tell the guest what it chose.
// Capped because each slot is a merged set - tens of thousands of entries for a
// real base - and a slot that is never reused is memory spent on nothing.
func NewFolder(layerDir string) *Folder {
	return &Folder{dir: layerDir, slots: min(max(runtime.NumCPU(), 2), maxFolds)}
}

// maxFolds bounds what the memo can cost.
//
// A merged set is roughly the manifest it came from, so eight slots over a
// 45k-entry base is tens of megabytes - affordable beside the layers themselves,
// and past this the chains are numerous enough that each is short.
const maxFolds = 8

// TreeOf is what a stack materialises to, or nothing. See DirStore.TreeOf,
// whose answer this must equal for every stack.
func (f *Folder) TreeOf(stack []ir.NodeID) (ir.NodeID, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	h, from := f.claim(stack)

	// **Counted as they go in**, so a failure part-way leaves nothing to
	// extend: Add applies entries as it reads them, and a fold holding half a
	// layer is a stack that never existed.
	applied := 0

	for _, id := range stack[from:] {
		m, err := os.ReadFile(ManifestPath(f.dir, id))
		if err != nil {
			// No manifest: a declaration, or a layer that arrived as opaque
			// bytes. Neither contributes paths to the merged tree.
			applied++

			continue
		}

		if !h.fold.Add(m) {
			f.drop(h)

			return ir.NodeID{}, false
		}

		applied++
		h.read++
	}

	h.stack = append(h.stack[:from:from], stack[from:from+applied]...)

	// A stack no part of which could be read is not guessed at: a tree digest
	// over nothing would be shared by every base in existence.
	if h.read == 0 {
		return ir.NodeID{}, false
	}

	return h.fold.Digest(), true
}

// claim is the fold this stack extends, and how much of it is already done.
//
// The longest held prefix wins, so a chain finds its own fold rather than a
// shorter one it would have to redo. A stack held exactly costs only its digest:
// the host remembers per stack and rarely asks twice, but a second build over
// one guest does. A chain that extends nothing takes the
// least recently used slot, which is the cost every stack paid before.
func (f *Folder) claim(stack []ir.NodeID) (*held, int) {
	best, at := -1, 0

	for i, h := range f.held {
		n := len(h.stack)
		if n > at && n <= len(stack) && slices.Equal(h.stack, stack[:n]) {
			best, at = i, n
		}
	}

	if best < 0 {
		h := &held{fold: layer.NewFold()}

		if len(f.held) >= f.slots {
			f.held = f.held[:len(f.held)-1] // evict the least recently used
		}

		f.held = append([]*held{h}, f.held...)

		return h, 0
	}

	h := f.held[best]
	f.held = append([]*held{h}, slices.Delete(slices.Clone(f.held), best, best+1)...)

	return h, at
}

// drop forgets a fold that failed part-way, which is a stack that never existed.
func (f *Folder) drop(h *held) {
	for i, other := range f.held {
		if other == h {
			f.held = slices.Delete(f.held, i, i+1)

			return
		}
	}
}
