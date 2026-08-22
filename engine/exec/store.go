package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// DirStore is a core.Store backed by a host directory, which is what every
// store is today.
//
// The port lives in core because two packages need it: this one places and
// squashes, and the fleet serves layers to peers out of the same store.
type DirStore string

// Has reports whether the layer's tree is there.
//
// An empty directory is a layer: a step that writes nothing produces an empty
// delta, and that is a perfectly good result to cache. Partial commits are
// prevented by writing under a temporary name and renaming, not by guessing
// from contents.
func (d DirStore) Has(id ir.NodeID) bool { return LayerStore(d).Has(id) }

// LayerPath is where the tree lives.
func (d DirStore) LayerPath(id ir.NodeID) string {
	return filepath.Join(string(d), "layers", id.String())
}

// Declaration reads what the image declared and files it as a stack element.
func (d DirStore) Declaration(layer ir.NodeID) ir.NodeID {
	return declarationFor(string(d), layer)
}

// Place files a captured tree under the digest of what it holds.
func (d DirStore) Place(staging string) (ir.NodeID, error) {
	return placeCaptured(string(d), staging)
}

// Squash merges a range of layers by hard-linking them into one directory.
//
// Links rather than copies: a layer is immutable once written, which is what
// makes it addressable, so a squash of ten gigabytes costs inodes and no bytes.
func (d DirStore) Squash(ctx context.Context, into ir.NodeID, rng []ir.NodeID) error {
	return squashInto(ctx, string(d), into, rng)
}

// Staging makes room beside the layers, creating the store on first use: a cold
// store has no directory yet, which is the common case rather than an error.
func (d DirStore) Staging(prefix string) (string, error) {
	at := filepath.Join(string(d), "layers")

	err := os.MkdirAll(at, 0o750)
	if err != nil {
		return "", fmt.Errorf("prepare the layer store: %w", err)
	}

	dir, err := os.MkdirTemp(at, prefix)
	if err != nil {
		return "", fmt.Errorf("make room in the layer store: %w", err)
	}

	return dir, nil
}

// Root is the directory this store occupies.
//
// Present only while the store is a directory: the callers that still need it
// are exactly the ones phase 1 has not converted, so it is the measure of how
// much is left.
func (d DirStore) Root() string { return string(d) }

// DirStore is a core.Store.
var _ core.Store = DirStore("")
