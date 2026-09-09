package store

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// indexSuffix names a layer's saved index, beside the layer it describes - the
// same arrangement as a layer's configuration and its declaration.
const indexSuffix = ".index"

// indexMagic and indexVersion say that a file is one of these and that it was
// written by a build that hashed paths the way this one does.
//
// **The version is the whole safety of changing hashPath.** An index written by
// a different hash is not corrupt, it is confidently wrong: it says a layer
// holds nothing it holds, and that is the answer which makes files vanish from
// a base and a stale cache entry look fresh. Bump this and every index on disk
// is ignored rather than believed.
const (
	indexMagic   = 0x45425849 // "EBXI"
	indexVersion = 1
)

// indexHeaderBytes is magic, version and count.
const indexHeaderBytes = 4 + 4 + 8

// saveIndex writes an index beside its layer, atomically.
//
// A neighbour and a rename, because a reader must never see half of one: a
// short index is a smaller set, and a smaller set answers "absent" for
// everything missing from it.
func saveIndex(at string, idx *layerIndex) error {
	sorted := make([]uint64, 0, len(idx.has))
	for h := range idx.has {
		sorted = append(sorted, h)
	}

	// Sorted so the file is a function of the layer and not of map iteration
	// order: two builds indexing one layer write the same bytes.
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	buf := make([]byte, indexHeaderBytes+8*len(sorted))
	binary.LittleEndian.PutUint32(buf[0:], indexMagic)
	binary.LittleEndian.PutUint32(buf[4:], indexVersion)
	binary.LittleEndian.PutUint64(buf[8:], uint64(len(sorted)))

	for i, h := range sorted {
		binary.LittleEndian.PutUint64(buf[indexHeaderBytes+8*i:], h)
	}

	tmp, err := os.CreateTemp(filepath.Dir(at), ".index-*")
	if err != nil {
		return fmt.Errorf("make room for a layer index: %w", err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = tmp.Write(buf)
	if err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write a layer index: %w", err)
	}

	err = tmp.Close()
	if err != nil {
		return fmt.Errorf("write a layer index: %w", err)
	}

	return os.Rename(tmp.Name(), at) //nolint:wrapcheck // the caller says which layer
}

// loadIndex reads a saved index, or nil for anything that is not exactly one.
//
// **Nil rather than empty, for every failure.** An empty index answers "absent"
// for every path in the layer, so a file that is absent, short, unreadable or
// written by another version has to be no index at all - which costs the walk
// that built it and never costs a wrong answer.
func loadIndex(at string) *layerIndex {
	b, err := os.ReadFile(at)
	if err != nil || len(b) < indexHeaderBytes {
		return nil
	}

	if binary.LittleEndian.Uint32(b[0:]) != indexMagic ||
		binary.LittleEndian.Uint32(b[4:]) != indexVersion {
		return nil
	}

	n := binary.LittleEndian.Uint64(b[8:])

	// The count is what catches truncation: a short file is a smaller set and
	// reads as a layer that holds less than it does.
	if uint64(len(b)-indexHeaderBytes) != n*8 {
		return nil
	}

	idx := &layerIndex{has: make(map[uint64]struct{}, n)}
	for i := range int(n) { //nolint:gosec // bounded by the length check above
		idx.has[binary.LittleEndian.Uint64(b[indexHeaderBytes+8*i:])] = struct{}{}
	}

	return idx
}

// forgetLayerIndex removes a layer's saved index, for a layer being collected.
//
// Best effort: the index is derived, so failing to remove it costs a file and
// never an answer. Called where a layer is removed, because a collector that
// took the directory and left the notes beside it would fill a store with them.
func forgetLayerIndex(layerPath string) {
	_ = os.Remove(layerPath + indexSuffix)
	indexed.Delete(layerPath)
}
