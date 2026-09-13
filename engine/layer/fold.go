package layer

import (
	"path"
	"strings"

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

	// root is the same set as a directory trie, carried across Add so that a
	// layer costs the directories it touched rather than all of them. Every
	// node holds the digest it was last given and whether that is still true.
	root *dir
}

// NewFold is the fold of the empty stack.
func NewFold() *Fold {
	return &Fold{merged: map[string]entry{}, root: newDir()}
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

	for _, p := range apply(f.merged, entries) {
		f.resync(p)
	}

	return true
}

// resync brings the trie back in line with the merged set at one path.
//
// Asked of the set rather than told, because apply resolves a layer against
// itself - a name written and then whited out within one layer is reported and
// is not there - and a caller that trusted the report would hold a tree the
// fold does not.
func (f *Fold) resync(p string) {
	if e, ok := f.merged[p]; ok {
		f.root.insert(strings.Split(path.Clean(p), "/"), e)

		return
	}

	f.root.remove(strings.Split(path.Clean(p), "/"))
}

// Digest is 𝜏, the tree the fold has reached, and does not consume it.
//
// Only the directories a layer moved are named again; everything else answers
// from the digest it was given last time. That is the whole saving - a step
// writes tens of paths into a base of tens of thousands.
func (f *Fold) Digest() ir.NodeID {
	b := builder{}

	return b.cached(f.root)
}

// Tree is the fold as a Merkle tree of directories, every node addressable.
//
// Names every node and keeps its bytes, which Digest does not: a key needs the
// name and shipping a subtree needs the encoding, and holding 26MB of them per
// 20k entries for every fold in the memo is not a cost a key should carry.
func (f *Fold) Tree() Tree {
	b := builder{nodes: map[ir.NodeID][]byte{}}
	t := Tree{nodes: b.nodes}
	t.root = b.digest(f.root)

	return t
}
