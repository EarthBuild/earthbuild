package layer

import (
	"sort"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Fold is a stack folded so far, extendable one layer at a time.
//
// **Because the base is folded once per step above it.** Κₜ (green paper 4.5a)
// asks what a step's base materialises to, and a linear target's step 𝑖 has a
// stack of 𝑖 layers - so folding each stack from scratch re-reads the bottom
// layer once per step. That bottom layer is the expensive one: a Rust deps
// layer is tens of thousands of entries and a step's own output is tens, and
// measured at 0.93µs an entry a 45k-entry base under fifty steps is two seconds
// of a build spent folding something that did not change.
//
// Extending is sound because application is a left fold: applying a layer to the
// merged set for 𝑏₁..𝑏ₙ₋₁ gives the merged set for 𝑏₁..𝑏ₙ, which is the same
// definition (4.5a) states. The saving is in not repeating the prefix, not in
// any different answer - store.Folder's tests check the two agree at every depth.
type Fold struct {
	merged map[string]entry
}

// NewFold is the fold of the empty stack.
func NewFold() *Fold {
	return &Fold{merged: map[string]entry{}}
}

// Add lays one more layer over the fold, reporting whether it could be read.
//
// A false leaves the fold holding part of the layer, which is a stack that
// never existed: the caller must discard it rather than extend it again.
func (f *Fold) Add(m []byte) bool {
	entries, err := decodeManifest(m)
	if err != nil {
		return false
	}

	apply(f.merged, entries)

	return true
}

// Digest is the tree the fold has reached, and does not consume it.
func (f *Fold) Digest() ir.NodeID {
	paths := make([]string, 0, len(f.merged))
	for p := range f.merged {
		paths = append(paths, p)
	}

	sort.Strings(paths)

	h := ir.NewHasher()
	h.Count(len(paths))

	for _, p := range paths {
		e := f.merged[p]
		e.hash(&h.Encoder, withoutTimes)
	}

	return h.Sum()
}
