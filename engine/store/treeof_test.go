package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A store folds a stack into the tree it materialises.
//
// **Once per stack, not once per layer.** Κₜ names a base by what it holds, and
// what a *stack* holds is the fold - a later layer winning, a whiteout removing
// a name, an opaque marker emptying what a directory inherited. Asking per
// layer gave a sequence, and a sequence distinguishes a stack Φ flattened from
// the stack it flattened.
//
// A declaration is a stack element and not a layer - it has no manifest to fold
// and contributes nothing to a merged tree - so it is skipped rather than
// refused, exactly as squashInto skips it.
func TestAStoreFoldsAStackIntoItsTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)

	lower := layerWith(t, root, 0, map[string]string{"a.txt": "one", "doomed.txt": "two"})
	upper := layerWith(t, root, 1, map[string]string{".wh.doomed.txt": ""})

	// What the same pair materialises to, as one layer.
	merged := layerWith(t, root, 2, map[string]string{"a.txt": "one"})

	stacked, ok := st.TreeOf([]ir.NodeID{lower, upper})
	if !ok {
		t.Fatal("a stack of two layers with manifests could not be folded")
	}

	alone, ok := st.TreeOf([]ir.NodeID{merged})
	if !ok {
		t.Fatal("a single layer could not be folded")
	}

	if stacked != alone {
		t.Errorf("a stack folded to %v and the tree it materialises to %v", stacked, alone)
	}
}

// A stack nobody has a manifest for is not guessed at.
func TestAStackWithNoManifestsIsNotFolded(t *testing.T) {
	t.Parallel()

	st := store.DirStore(t.TempDir())

	if _, ok := st.TreeOf([]ir.NodeID{{1}, {2}}); ok {
		t.Error("a stack with no manifests anywhere was given a tree digest")
	}
}

// layerWith files a layer with a manifest beside it and returns its id.
func layerWith(t *testing.T, root string, n int, files map[string]string) ir.NodeID {
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

	m, err := layer.Manifest(at)
	if err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(root, id, m)

	return id
}
