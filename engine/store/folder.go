package store

import (
	"os"
	"slices"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Folder answers TreeOf, reusing the fold it did last time where it can.
//
// **A build asks about a ladder, not about unrelated stacks.** Κₜ consults a
// step's base, and step 𝑖 of a target has step 𝑖-1's stack with one layer on
// top - so consecutive questions share every layer but the last. Holding the
// fold of the last stack answered turns the build's bill from "every step pays
// for the whole base" into "the base is folded once".
//
// One prefix is held rather than a cache of many, because each one costs a
// merged set - tens of thousands of entries for a real base - and a second slot
// buys nothing on a chain. A branch that does not extend the held prefix is
// folded from scratch, which is what every stack cost before this existed, so
// the fallback is the old behaviour rather than a penalty.
type Folder struct {
	dir string

	mu    sync.Mutex
	stack []ir.NodeID
	fold  *layer.Fold
	read  int // manifests decoded into fold; a stack of none is not a tree
}

// NewFolder holds no stack yet.
func NewFolder(layerDir string) *Folder {
	return &Folder{dir: layerDir}
}

// TreeOf is what a stack materialises to, or nothing. See DirStore.TreeOf,
// whose answer this must equal for every stack.
func (f *Folder) TreeOf(stack []ir.NodeID) (ir.NodeID, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	from := 0
	if f.fold != nil && len(stack) > len(f.stack) && slices.Equal(f.stack, stack[:len(f.stack)]) {
		from = len(f.stack)
	} else {
		f.fold, f.stack, f.read = layer.NewFold(), nil, 0
	}

	// **Held before read**, so a failure part-way leaves nothing to extend: Add
	// applies entries as it goes, and a fold holding half a layer is a stack
	// that never existed.
	held := 0

	for _, id := range stack[from:] {
		m, err := os.ReadFile(ManifestPath(f.dir, id))
		if err != nil {
			// No manifest: a declaration, or a layer that arrived as opaque
			// bytes. Neither contributes paths to the merged tree.
			held++

			continue
		}

		if !f.fold.Add(m) {
			f.fold, f.stack, f.read = nil, nil, 0

			return ir.NodeID{}, false
		}

		held++
		f.read++
	}

	f.stack = append(f.stack[:from:from], stack[from:from+held]...)

	// A stack no part of which could be read is not guessed at: a tree digest
	// over nothing would be shared by every base in existence. Counted as the
	// manifests go in rather than restatted, so the answer costs no syscalls
	// beyond the ones the fold already made.
	if f.read == 0 {
		return ir.NodeID{}, false
	}

	return f.fold.Digest(), true
}
