package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A stack holding a manifest that cannot be decoded is not a tree.
//
// **The zero digest is not an answer.** Κₜ (green paper 4.5a) keys on what a
// stack materialises to, and a fold that could not decode one of its layers does
// not know that. Reporting it as a tree anyway gives every base with a corrupt
// manifest one key, so a step over one of them is served the result of a step
// over another - a false hit, which is the one thing Λ may never do (I3).
//
// An unreadable manifest is *skipped* and a corrupt one is not, because they say
// different things: a stack element with no manifest beside it is a declaration
// and contributes no paths, while bytes that are present and will not decode
// mean the fold does not know what the layer held.
func TestAStackWithACorruptManifestIsNotATree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)

	good := layerWithManifest(t, root, map[string]string{"a.txt": "one"})
	other := layerWithManifest(t, root, map[string]string{"b.txt": "two"})

	// A layer whose manifest is present but will not decode.
	corrupt := ir.NodeID{0xc0, 0x11, 0xab}
	if err := os.MkdirAll(filepath.Dir(store.ManifestPath(root, corrupt)), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(store.ManifestPath(root, corrupt), []byte("not a manifest"), 0o600); err != nil {
		t.Fatal(err)
	}

	one, okOne := st.TreeOf([]ir.NodeID{good, corrupt})
	if okOne {
		t.Fatalf("a stack holding an undecodable manifest folded to %v, so a key"+
			"\n  would be derived from a fold that did not happen", one)
	}

	two, okTwo := st.TreeOf([]ir.NodeID{other, corrupt})
	if okTwo {
		t.Fatal("a stack holding an undecodable manifest folded")
	}

	if one == two && okOne && okTwo {
		t.Error("two bases with nothing in common share a tree")
	}
}

// layerWithManifest writes a layer's manifest into the store and returns its id.
func layerWithManifest(t *testing.T, root string, files map[string]string) ir.NodeID {
	t.Helper()

	dir := t.TempDir()

	for name, body := range files {
		at := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(at, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	took, err := layer.Take(dir)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(store.ManifestPath(root, took.ID)), 0o750); err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(root, took.ID, m)

	return took.ID
}
