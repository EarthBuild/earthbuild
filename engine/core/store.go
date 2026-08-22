package core

import (
	"context"

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

	// Place files a captured tree and returns the identity it landed under.
	//
	// **The name is not the caller's to choose.** A layer is named by the digest
	// of what it holds, so a store that filed what arrived under the name it was
	// asked for would serve corruption for ever after, and every key derived
	// from that base would name something else (green paper §5.3).
	Place(staging string) (ir.NodeID, error)

	// Squash merges a range of layers into one, oldest first, under an identity
	// derived from the range (green paper 4.8).
	//
	// Idempotent, because the identity is derived rather than chosen: a squash
	// that is already there is already right, whoever built it.
	Squash(ctx context.Context, into ir.NodeID, rng []ir.NodeID) error

	// Staging is somewhere to build a layer before it has a name.
	//
	// The store's own, not the caller's: a layer arrives by being renamed into
	// place, and a rename across filesystems is a copy of the largest thing this
	// engine moves. It is also why the name is a prefix rather than a path -
	// where the room comes from is the store's business, and on a disk it is not
	// a path the caller could have written.
	Staging(prefix string) (string, error)
}
