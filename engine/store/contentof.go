package store

import (
	"os"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// ContentSuffix names the content identity kept beside a layer.
//
// The fold that produces it is 591ns an entry, so about 59ms for a 100k-entry
// base: affordable once, and not once per step. A note beside the layer makes
// it once per machine instead, the way `.leaked` and `.unmarked` record what a
// capture learned.
const ContentSuffix = ".content"

// ContentOf is a layer's identity with its timestamps excluded, or nothing.
//
// **What a base can be keyed on across a rebuild.** A layer's own id hashes
// mtimes (I8), so a deterministic step built twice gives two ids; the content
// identity is unmoved by that, and Κ_c names a base by it. See
// core.DeriveContentKey.
//
// Answered from the manifest already beside the layer - "the bytes the digest
// is already over" - so nothing is walked. A layer with no manifest is not
// guessed at: false leaves the caller with the key it had, and a placeholder
// would let two bases holding anything at all share one.
func (d DirStore) ContentOf(id ir.NodeID) (ir.NodeID, bool) {
	at := d.LayerPath(id) + ContentSuffix

	if noted, err := os.ReadFile(at); err == nil {
		if parsed, err := ir.ParseNodeID(string(noted)); err == nil {
			return parsed, true
		}
	}

	m, err := os.ReadFile(ManifestPath(string(d), id))
	if err != nil {
		return ir.NodeID{}, false
	}

	content, err := layer.ContentFromManifest(m)
	if err != nil {
		return ir.NodeID{}, false
	}

	// Best effort, as NoteManifest is: a note that cannot be written costs the
	// next build one fold, which is where this was before the note existed.
	_ = os.WriteFile(at, []byte(content.String()), 0o600)

	return content, true
}
