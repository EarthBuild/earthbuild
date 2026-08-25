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

	// Populated reports whether a layer is there *and* has something in it.
	//
	// Distinct from Has, which counts an empty layer as present - and correctly,
	// because a step that writes nothing produces an empty delta worth caching.
	// This asks a different question: an image that unpacked to nothing did not
	// unpack, so the entry naming it is a claim to re-check rather than a base
	// to build on.
	//
	// **Only ever of a whole merged image.** Applied to one layer of a stack it
	// is simply wrong: images ship empty layers - `golang:1.26-alpine` stacks
	// five and the topmost holds nothing - so one of them made an entire
	// remembered stack read as absent, and every warm build re-fetched all five
	// (8.1s against 0.2s). Reach for Has.
	Populated(id ir.NodeID) bool

	// NoteUnmarked records that a layer carries no whiteout markers, so nothing
	// has to walk it to find that out again (E531).
	NoteUnmarked(id ir.NodeID)

	// AdoptConfig takes an image configuration as belonging to a layer.
	//
	// Beside the tree rather than in it: what an image declares is not part of
	// what it ships, so putting it inside would make the layer no longer what
	// its digest says. Kept only if the layer has none - two builds placing one
	// image both arrive with a copy, and the first is as good as the second.
	AdoptConfig(id ir.NodeID, from string) error

	// PutNamed files a staged tree under an identity the caller chose.
	//
	// **Distinct from Place, and the distinction is not a convenience.** A layer
	// is normally named by the digest of what it holds, which is what makes two
	// machines agree without asking. A local context cannot be: it is named by
	// the node that asked for it, because what it holds is a copy of somebody's
	// working directory and the *request* is its identity.
	//
	// Whole or not at all. A tree built directly under its final name leaves,
	// when a copy fails half way, a directory that Has reports as present - and
	// a later build stands on it.
	PutNamed(id ir.NodeID, staging string) error
}
