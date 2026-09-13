package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// ManifestSuffix names the manifest kept beside a layer.
//
// A manifest lists every path the layer holds with its mode, ownership, times,
// size and - for a regular file - the digest of its contents.
//
// **It attests to a layer named by its own digest**: `layer.ManifestID` over
// these bytes is that layer's identity, so a manifest cannot describe paths the
// layer does not have without ceasing to be its manifest. A layer filed under a
// name the caller gave - a build context, whose identity is the plan's - has a
// manifest too, and that one is a note rather than a proof. A reader that needs
// the proof must check the hash; a reader that only needs to be told what a file
// holds, as `COPY --sync` does, need not.
const ManifestSuffix = ".manifest"

// ManifestPath is where a layer's manifest lives.
func ManifestPath(layerDir string, id ir.NodeID) string {
	return filepath.Join(layerDir, "layers", id.String()) + ManifestSuffix
}

// NoteManifest writes down what the capture already worked out.
//
// **The walk that produced the layer read every byte of it.** Everything in a
// manifest is a by-product of that walk, so writing it costs an encode and one
// file - about a tenth of a percent of the layer - while recomputing it later
// costs the whole walk again: 463 ms and 898 MB of reads, measured on this
// repository's own rust base layer.
//
// Best effort, exactly as `noteUnmarked` is. A manifest that cannot be written
// costs a later reader one walk, which is what every reader did before this
// existed; failing a build over it would be absurd.
//
// Written beside the layer and then renamed, because a half-written manifest
// under its final name is a file that claims to attest and does not.
func NoteManifest(layerDir string, id ir.NodeID, manifest []byte) {
	if len(manifest) == 0 {
		return
	}

	at := ManifestPath(layerDir, id)

	tmp, err := os.CreateTemp(filepath.Dir(at), ".manifest-*")
	if err != nil {
		return
	}

	_, err = tmp.Write(manifest)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		_ = os.Remove(tmp.Name())

		return
	}

	err = os.Rename(tmp.Name(), at)
	if err != nil {
		_ = os.Remove(tmp.Name())

		return
	}

	noteNodesOf(layerDir, manifest)
}

// noteNodesOf files the directory nodes the manifest already describes.
//
// **Kept with the manifest because they are the same fact.** A store holding the
// manifest and not the nodes answers "I lack every subtree" about a base it has
// in full, and a sender believes it - so the saving the tree exists for is lost
// by a store that is merely incomplete rather than wrong.
//
// Costs about what the manifest costs: measured at 95% of its bytes, which is
// roughly a tenth of a percent of the layer, and the fold that produces them is
// a millisecond against the walk's several hundred.
//
// Best effort, exactly as the manifest's own write is. Nodes that could not be
// written cost a sender a larger transfer, which is what every transfer did
// before this existed.
func noteNodesOf(layerDir string, manifest []byte) {
	f := layer.NewFold()
	if !f.Add(manifest) {
		return
	}

	_ = DirStore(layerDir).NoteNodes(f.Tree())
}

// ReadManifest returns a layer's manifest, and whether one was kept.
//
// Absent is ordinary: a layer stored before this existed has none, and so does
// one whose note could not be written. Every caller must be able to fall back to
// walking, which is what it did before.
func ReadManifest(layerDir string, id ir.NodeID) ([]byte, bool, error) {
	b, err := os.ReadFile(ManifestPath(layerDir, id))
	if os.IsNotExist(err) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("read the manifest for %v: %w", id, err)
	}

	return b, true, nil
}
