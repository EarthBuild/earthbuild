package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A layer's content identity is answered from the manifest beside it, and then
// remembered.
//
// The fold is 591ns an entry - about 59ms for a 100k-entry base - so asking
// once per build per layer is affordable and asking once per *step* is not.
// Written beside the layer as `.leaked` and `.unmarked` are, so it is computed
// once per machine rather than once per build.
func TestAStoreAnswersALayersContentFromItsManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)

	// A tree, and the layer it would be.
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	took, err := layer.Take(tree)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(tree)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(st.LayerPath(took.ID)), 0o750); err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(root, took.ID, m)

	got, ok := st.ContentOf(took.ID)
	if !ok {
		t.Fatal("a layer with a manifest beside it could not be asked its content")
	}

	if got != took.Content {
		t.Errorf("answered %v, the capture said %v", got, took.Content)
	}

	// Asked again with the manifest gone: the note is what answers.
	if err := os.Remove(store.ManifestPath(root, took.ID)); err != nil {
		t.Fatal(err)
	}

	again, ok := st.ContentOf(took.ID)
	if !ok || again != took.Content {
		t.Errorf("the second answer was %v/%v, so nothing was remembered and the"+
			"\n  fold would be repaid on every build", again, ok)
	}
}

// A layer nobody has a manifest for is not guessed at.
func TestAStoreWithNoManifestSaysSo(t *testing.T) {
	t.Parallel()

	st := store.DirStore(t.TempDir())

	if _, ok := st.ContentOf(layer.Capture{}.ID); ok {
		t.Error("a layer with no manifest was given a content identity anyway")
	}
}
