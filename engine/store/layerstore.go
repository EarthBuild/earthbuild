package store

import (
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// LayerStore is a core.BlobStore backed by a directory of layers.
//
// It answers one question - is this layer actually here? - and that question is
// what keeps a cache hit honest. An action-cache entry is a *claim*; the claim
// is only usable if the result it names is present, so a layer lost to a GC, a
// partial copy or a truncated transfer must produce a miss rather than a base
// that does not exist (green paper §5.2).
//
// The failure it prevents is asymmetric: a miss costs time, and a hit on an
// absent layer costs correctness.
type LayerStore string

// Has reports whether a layer is present.
//
// Presence, not integrity: verifying the contents would mean rehashing the tree
// on every lookup, which is a full capture on the hot path. The self-verifying
// property lives in the blob store (§2.1); here the concern is that the layer
// is there at all.
//
// **An empty directory is a layer.** A step that writes nothing - `true`, a
// no-op make, a test that only reads - produces an empty delta, and that is a
// perfectly good result to cache. Treating emptiness as absence made every such
// step miss forever. Partial commits are prevented by writing the layer under a
// temporary name and renaming it into place, not by guessing from its contents.
func (s LayerStore) Has(id ir.NodeID) bool {
	if s == "" {
		return false
	}

	fi, err := os.Stat(s.Path(id))

	return err == nil && fi.IsDir()
}

// Path is where a layer's tree lives.
func (s LayerStore) Path(id ir.NodeID) string {
	return filepath.Join(string(s), "layers", id.String())
}

// Verify rehashes a layer and reports whether it matches the digest naming it.
//
// Not called on lookup, deliberately. Within one trust domain the store is
// written only by this engine (green paper A5), and rehashing every base on
// every hit would put a full capture on the hot path - the cost the cache exists
// to avoid.
//
// It is required at exactly one boundary: a layer arriving from *outside* the
// trust domain - a fleet peer, a shared cache, a restored archive - is
// unauthenticated data until this returns true (§5.3). A store that serves
// different bytes at a digest is otherwise undetectable, because a digest-named
// directory is trusted for what it is named rather than for what it contains.
//
// **[GAP]** nothing calls this yet: there is no import path, because there is no
// fleet transport. It exists so that the check is defined before the thing that
// needs it, rather than being retrofitted onto a transport that already works.
func (s LayerStore) Verify(id ir.NodeID) bool {
	c, err := layer.Take(filepath.Join(string(s), "layers", id.String()))
	if err != nil {
		return false
	}

	return c.ID == id
}
