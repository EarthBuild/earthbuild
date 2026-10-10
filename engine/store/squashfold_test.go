package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// layerHolding files a layer in the store and returns its id.
func layerHolding(t *testing.T, root string, n int, files map[string]string) ir.NodeID {
	t.Helper()

	id := ir.NodeID{byte(n + 1)}

	at := filepath.Join(root, "layers", id.String())
	if err := os.MkdirAll(at, 0o750); err != nil {
		t.Fatal(err)
	}

	for name, content := range files {
		p := filepath.Join(at, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return id
}

func manifestOfLayer(t *testing.T, root string, id ir.NodeID) []byte {
	t.Helper()

	m, err := layer.Manifest(filepath.Join(root, "layers", id.String()))
	if err != nil {
		t.Fatal(err)
	}

	return m
}

// A flattened stack and the stack it flattened fold to one tree.
//
// **The case Κₜ cannot see and 𝜏 is for.** Φ (green paper 4.8) squashes when a
// stack nears 𝑛ₘₐₓ, and 𝑛ₘₐₓ is not a constant: `mount(2)` reads its options
// from a single page, so it "depends on the length of the layer paths" and is
// "the smallest bound the materialiser is subject to". Two machines with
// different store paths therefore flatten the *same target* at different
// points. A per-layer sequence makes the two stacks different keys -
// while the filesystem a step sees is identical.
//
// Measured elsewhere: a 70-step target reports `7 flattened`, so this is a
// horizon every growing build crosses rather than a corner.
//
// The squash here is the engine's own, markers and all, because the shape that
// matters is the one squashInto actually produces: a concatenated range holding
// a file from one member and the whiteout that deleted it from a later one.
func TestAFlattenedStackFoldsToTheSameTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Five layers, including a deletion of something an earlier one wrote -
	// which is what puts a marker beside its file once the range is squashed.
	ids := []ir.NodeID{
		layerHolding(t, root, 0, map[string]string{"keep.txt": "one", "doomed.txt": "two"}),
		layerHolding(t, root, 1, map[string]string{"d/nested.txt": "three"}),
		layerHolding(t, root, 2, map[string]string{".wh.doomed.txt": ""}),
		layerHolding(t, root, 3, map[string]string{"keep.txt": "overwritten"}),
		layerHolding(t, root, 4, map[string]string{"last.txt": "four"}),
	}

	// Machine A flattens the oldest three; machine B, with shorter store paths,
	// does not flatten at all.
	// Any distinct identity: what the squashed layer is *called* is core's
	// business (SquashID) and does not bear on whether it holds the same bytes.
	squashed := ir.NodeID{0xfe}
	if err := squashInto(context.Background(), root, squashed, ids[:3]); err != nil {
		t.Fatal(err)
	}

	flat := append([]ir.NodeID{squashed}, ids[3:]...)

	// Κₜ names a base by the sequence, so the two are different keys.
	if len(flat) == len(ids) {
		t.Fatal("the two stacks have the same shape, so this tests nothing")
	}

	var flatM, fullM [][]byte

	for _, id := range flat {
		flatM = append(flatM, manifestOfLayer(t, root, id))
	}

	for _, id := range ids {
		fullM = append(fullM, manifestOfLayer(t, root, id))
	}

	got, okFlat := layer.TreeFromManifests(flatM)
	want, okFull := layer.TreeFromManifests(fullM)

	if !okFlat || !okFull {
		t.Fatal("a stack this test squashed could not be folded")
	}

	if got != want {
		t.Errorf("the flattened stack folded to %v and the original to %v"+
			"\n  they materialise the same filesystem, so a cache keyed on the"+
			"\n  fold must not be able to tell them apart - which is the whole"+
			"\n  reason for folding rather than naming the sequence", got, want)
	}
}
