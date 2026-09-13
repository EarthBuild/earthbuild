package store

import (
	"os"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// TreeOf is what a stack materialises to, or nothing.
//
// **The base named by what it holds.** Κₜ (green paper 4.5a) keys on this, and
// asking it of a *stack* rather than of each layer is the point: a sequence of
// per-layer answers distinguishes the stack Φ (4.8) flattened from the stack it
// flattened, and 𝑛ₘₐₓ is materialiser-dependent, so two machines flatten one
// target differently. The fold does not.
//
// Answered from the manifests already beside the layers - "the bytes the digest
// is already over" - so nothing is walked. A declaration is skipped rather than
// refused: it is a stack element and not a layer, has no manifest to fold, and
// contributes nothing to a merged tree, exactly as squashInto skips it.
//
// A stack no part of which can be read is not guessed at: false leaves the
// caller with the key it had, and a tree digest over nothing would be shared by
// every base in existence.
func (d DirStore) TreeOf(stack []ir.NodeID) (ir.NodeID, bool) {
	manifests := make([][]byte, 0, len(stack))

	for _, id := range stack {
		m, err := os.ReadFile(ManifestPath(string(d), id))
		if err != nil {
			// No manifest: a declaration, or a layer that arrived as opaque
			// bytes. Neither contributes paths to the merged tree.
			continue
		}

		manifests = append(manifests, m)
	}

	if len(manifests) == 0 {
		return ir.NodeID{}, false
	}

	return layer.TreeFromManifests(manifests), true
}
