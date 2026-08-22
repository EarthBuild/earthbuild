package exec

import (
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Store is what this engine needs of a layer store.
//
// **A surface rather than a path.** Six capabilities reached into the store by
// joining a path and walking it, which works only because the store is a host
// directory - and a store on a block device cannot be walked from the host at
// all. Each becomes an operation here before the storage moves, so the change
// that matters is reviewable one method at a time rather than as a rewrite
// (E541, and the route in the plan).
//
// The methods are the ones callers actually use, added as they are converted.
// An interface that guessed at the eventual set would be wrong in both
// directions.
type Store interface {
	// Has reports whether a layer is present. Presence, not integrity: a claim
	// naming a layer that is not there must miss rather than serve a base that
	// does not exist (green paper §5.2).
	Has(id ir.NodeID) bool

	// LayerPath is where a layer's tree lives.
	//
	// **The method that phase 2 deletes.** A caller wanting a path is a caller
	// assuming the store is reachable as a filesystem, so every use of this is a
	// place that has to become an operation before the store can be a disk. It
	// is here to make those places countable.
	LayerPath(id ir.NodeID) string

	// Declaration is what the image that produced a layer declared, as a stack
	// element, or the zero identity when it declared nothing (§3.2a).
	Declaration(layer ir.NodeID) ir.NodeID
}

// DirStore is a Store backed by a host directory, which is what every store is
// today.
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

// Root is the directory this store occupies.
//
// Present only while the store is a directory: the callers that still need it
// are exactly the ones phase 1 has not converted, so it is the measure of how
// much is left.
func (d DirStore) Root() string { return string(d) }
