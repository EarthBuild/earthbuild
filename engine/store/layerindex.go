package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// layerIndex is the set of paths a layer holds, as hashes.
//
// **Because a lookup walks every layer.** stackView.Digest probes each layer in
// turn until one answers, so a path living in the base image is looked for in
// every layer above it first - two syscalls each, and nearly all of them
// ENOENT. With 6299 observed paths over a stack at the 64-layer flatten
// threshold that is some 800,000 syscalls: 4.0s inside a guest against a
// millisecond on a host whose dentry cache is warm.
//
// Hashes rather than paths, because the set is consulted and never enumerated,
// and eight bytes a path keeps a large base image's index in megabytes rather
// than hundreds of them.
//
// **A collision is safe and an omission is not.** A false positive costs the
// stat that would have happened anyway; a false negative loses a file and
// silently changes what a build sees. So this answers "may have" - the caller
// must still ask the filesystem - and a layer that cannot be read has no index
// at all rather than an empty one.
type layerIndex struct {
	has map[uint64]struct{}
}

// mayHave reports whether the layer might hold a path, relative to its root.
func (i *layerIndex) mayHave(rel string) bool {
	_, ok := i.has[hashPath(rel)]

	return ok
}

// hashPath is FNV-1a, which is a function of the path and of nothing else.
//
// **Not maphash, whose seed is per-process.** An index is written beside its
// layer and read by later builds, so a hash that varied between processes would
// produce a file saying the layer holds nothing it holds - and "holds nothing"
// is the answer that loses files. Changing this invalidates every index on
// disk, which is what indexVersion is for.
func hashPath(rel string) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)

	h := uint64(offset)
	for i := range len(rel) {
		h ^= uint64(rel[i])
		h *= prime
	}

	return h
}

// indexed remembers what has been walked. A layer is content-addressed and
// immutable, so an index of one is good for as long as this process lives -
// and with a guest that now outlives a build, that is across builds.
var indexed sync.Map //nolint:gochecknoglobals // keyed by layer root, immutable

// indexOfLayer walks a layer once and remembers what it holds, or returns nil
// where it cannot be read.
//
// Nil rather than empty is the whole safety of this: an empty index answers
// "absent" for every path, which would make a layer's files invisible.
func indexOfLayer(root string) *layerIndex {
	if got, ok := indexed.Load(root); ok {
		idx, _ := got.(*layerIndex)

		return idx
	}

	// **Beside the layer, because the walk must happen once and not once a
	// build.** An agent is started fresh for every build - which is what stops
	// one build inheriting another's memory - so an index held only in memory
	// is rebuilt every time and pays for itself and no more: measured at 1.39s
	// of L2 either way. A layer is content-addressed and immutable, so a file
	// written beside it is good for ever.
	at := root + indexSuffix

	idx := loadIndex(at)
	if idx == nil {
		idx = walkLayer(root)

		// Best effort: a store nobody may write to is a slower build, not a
		// failed one, and the index is only ever an accelerator.
		if idx != nil {
			_ = saveIndex(at, idx)
		}
	}

	// Stored even when nil, so a missing layer is not walked again on every
	// path of every step.
	actual, _ := indexed.LoadOrStore(root, idx)

	got, _ := actual.(*layerIndex)

	return got
}

// walkLayer reads a layer's paths, or nil if it cannot be read in full.
//
// **All or nothing.** A walk that gave up halfway would produce an index that
// says a file is absent when it is there, and the caller trusts "absent".
func walkLayer(root string) *layerIndex {
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return nil
	}

	idx := &layerIndex{has: map[uint64]struct{}{}}

	err = filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}

		if rel != "." {
			idx.has[hashPath(rel)] = struct{}{}
		}

		return nil
	})
	if err != nil {
		return nil
	}

	return idx
}

// mayTouch reports whether a layer could have anything to say about a path:
// the file itself, a marker deleting it, or an opaque marker on any directory
// above it.
//
// **All three, because `deleted` consults all three.** A layer holding an
// opaque marker on an ancestor hides the path even though it holds neither the
// path nor a whiteout for it - and skipping such a layer would let a lower
// one answer, which is a file reappearing after it was deleted and a cache hit
// that is simply wrong. The index exists to save syscalls, and there is no
// saving worth that.
func (i *layerIndex) mayTouch(rel string) bool {
	if i.mayHave(rel) || i.mayHave(whiteoutOf(rel)) {
		return true
	}

	for d := filepath.Dir(rel); d != "." && d != string(filepath.Separator); d = filepath.Dir(d) {
		if i.mayHave(filepath.Join(d, whOpaque)) {
			return true
		}
	}

	return false
}

// whiteoutOf is the marker that would delete a path in a layer.
func whiteoutOf(rel string) string {
	return filepath.Join(filepath.Dir(rel), whPrefix+filepath.Base(rel))
}
