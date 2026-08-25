// Package store is σ: the layer store, and everything that puts a tree into it.
//
// Split out of the executor because it has two callers on opposite sides of the
// sandbox boundary. The host places images and build contexts; the guest places
// what a step captured, and cannot import the executor that used to own this
// code. Under a shared directory that split was invisible - both sides opened
// the same paths - and it is the split the disk makes real (E541, E542).
//
// What lives here answers "where does a layer live and how does one get there".
// What does not: pulling an image, running a step, or deciding which layer is
// wanted, all of which are the executor's.
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
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
	return placeCaptured(string(d), staging, Placement{})
}

// Placement is what an unpacker learned on the way past, so the store need not
// rediscover it - or, in the case of ownership, cannot.
type Placement struct {
	// Digests is each regular file's content digest, keyed by slash-separated
	// path. A path this does not name is read as before, so it is a read
	// skipped and never a different answer accepted (E653).
	Digests map[string]ir.NodeID
	// Owners is the archive's account of who owns each path.
	//
	// **Not an optimisation but a correction.** An unprivileged unpack cannot
	// grant the archive's ownership, so the disk says the builder owns what the
	// image says root owns - and on BSD a new file takes the enclosing
	// directory's group, so the layer's name depended on where the store lived.
	// The declaration settles it before the digest is taken (E313, E656).
	Owners map[string]layer.Owner
}

// PlaceAs is Place told what the unpacker already knows.
func (d DirStore) PlaceAs(staging string, p Placement) (ir.NodeID, error) {
	return placeCaptured(string(d), staging, p)
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

// Populated reports whether the layer is there and holds something.
func (d DirStore) Populated(id ir.NodeID) bool { return Populated(d.LayerPath(id)) }

// NoteUnmarked records that a layer carries no whiteout markers.
func (d DirStore) NoteUnmarked(id ir.NodeID) { noteUnmarked(d.LayerPath(id)) }

// AdoptConfig moves a configuration to sit beside its layer.
func (d DirStore) AdoptConfig(id ir.NodeID, from string) error {
	at := d.LayerPath(id) + ConfigSuffix

	// Only if there is none. Two builds placing the same image both arrive with
	// a copy, and whichever got there first is as good as this one.
	_, err := os.Stat(at)
	if err == nil {
		return nil
	}

	err = os.Rename(from, at)
	if err != nil {
		return fmt.Errorf("file the configuration for layer %v: %w", id, err)
	}

	return nil
}

// PutNamed renames a staged tree into place under the given identity.
//
// Already there is success, not a conflict: two builds may produce the same
// context, and the one that arrived first is as good as this one. The loser's
// staging is removed rather than renamed over, because a rename onto a directory
// fails and because what is there has been built exactly as carefully.
func (d DirStore) PutNamed(id ir.NodeID, staging string) error {
	err := Publish(string(d), id, staging)
	if err != nil {
		return err
	}

	// Gone already on the winning path; on the losing one this is what
	// "already there" costs.
	_ = os.RemoveAll(staging)

	return nil
}

// Root is the directory this store occupies.
//
// Present only while the store is a directory: the callers that still need it
// are exactly the ones phase 1 has not converted, so it is the measure of how
// much is left.
func (d DirStore) Root() string { return string(d) }

// DirStore is a core.Store.
var _ core.Store = DirStore("")
