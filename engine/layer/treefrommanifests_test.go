package layer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// manifestOf is the manifest of a tree built from a description.
func manifestOf(t *testing.T, files map[string]string) []byte {
	t.Helper()

	root := t.TempDir()

	for name, content := range files {
		at := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(at, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	m, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	return m
}

// Two stacks that materialise the same tree have one digest.
//
// **The claim the tier would rest on.** `contents(𝑏)` is a sequence, so two
// stacks that differ in how they were assembled differ as keys however
// identical the filesystem they produce - a flattened stack and its original
// (Φ, 4.8), two branches that converge, independent steps written in either
// order. 𝜏 folds the stack into the tree it materialises, so those agree.
func TestTwoStacksThatMaterialiseTheSameTreeAgree(t *testing.T) {
	t.Parallel()

	// One layer holding both files.
	together := layer.TreeFromManifests([][]byte{
		manifestOf(t, map[string]string{"a.txt": "one", "dir/b.txt": "two"}),
	})

	// The same tree, assembled in two layers - which is what Φ undoes and what
	// a differently-ordered build produces.
	apart := layer.TreeFromManifests([][]byte{
		manifestOf(t, map[string]string{"a.txt": "one"}),
		manifestOf(t, map[string]string{"dir/b.txt": "two"}),
	})

	if together != apart {
		t.Errorf("one layer gave %v and two gave %v, for the same tree"+
			"\n  this is the whole claim: a stack folded to what it materialises",
			together, apart)
	}
}

// A later layer overwriting an earlier one is the later one.
func TestALaterLayerWins(t *testing.T) {
	t.Parallel()

	overwritten := layer.TreeFromManifests([][]byte{
		manifestOf(t, map[string]string{"a.txt": "first"}),
		manifestOf(t, map[string]string{"a.txt": "second"}),
	})

	only := layer.TreeFromManifests([][]byte{
		manifestOf(t, map[string]string{"a.txt": "second"}),
	})

	if overwritten != only {
		t.Error("a file written twice did not fold to the later write")
	}
}

// And two trees that differ still differ.
//
// The companion, because agreeing about everything is satisfiable by a constant.
func TestDifferentTreesDisagree(t *testing.T) {
	t.Parallel()

	one := layer.TreeFromManifests([][]byte{manifestOf(t, map[string]string{"a.txt": "one"})})
	two := layer.TreeFromManifests([][]byte{manifestOf(t, map[string]string{"a.txt": "two"})})

	if one == two {
		t.Error("two trees holding different bytes shared a digest")
	}
}
