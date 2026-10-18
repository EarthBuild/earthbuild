package layer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// mustFold folds a stack and asserts it could be folded.
//
// Every caller wants the digest and none wants the zero one: a fold that could
// not decode a manifest reports so, and a test that dropped that report would
// pass on a digest nobody computed.
func mustFold(t *testing.T, ms [][]byte) ir.NodeID {
	t.Helper()

	got, ok := layer.TreeFromManifests(ms)
	if !ok {
		t.Fatal("a stack of manifests this test wrote could not be folded")
	}

	return got
}

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
// **The claim the tier would rest on.** A sequence of per-layer identities makes
// two stacks that differ in how they were assembled differ as keys however
// identical the filesystem they produce - a flattened stack and its original
// (Φ, 4.8), two branches that converge, independent steps written in either
// order. 𝜏 folds the stack into the tree it materialises, so those agree.
func TestTwoStacksThatMaterialiseTheSameTreeAgree(t *testing.T) {
	t.Parallel()

	// One layer holding both files.
	together := mustFold(t, [][]byte{
		manifestOf(t, map[string]string{"a.txt": "one", "dir/b.txt": "two"}),
	})

	// The same tree, assembled in two layers - which is what Φ undoes and what
	// a differently-ordered build produces.
	apart := mustFold(t, [][]byte{
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

	overwritten := mustFold(t, [][]byte{
		manifestOf(t, map[string]string{"a.txt": "first"}),
		manifestOf(t, map[string]string{"a.txt": "second"}),
	})

	only := mustFold(t, [][]byte{
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

	one := mustFold(t, [][]byte{manifestOf(t, map[string]string{"a.txt": "one"})})
	two := mustFold(t, [][]byte{manifestOf(t, map[string]string{"a.txt": "two"})})

	if one == two {
		t.Error("two trees holding different bytes shared a digest")
	}
}

// A whiteout beside the file it deletes, in one layer, still deletes it.
//
// **The shape a squash produces.** `squashInto` concatenates a range by linking
// trees over one another and leaves the markers for a later reader to apply, so
// a range where one layer wrote `foo` and a later one deleted it yields a single
// layer holding *both* `foo` and `.wh.foo`. `stackView.Digest` gets this right
// by asking `deleted(root, rel)` before looking for the file in the same root;
// a fold that walks entries in path order sees `.wh.foo` first, deletes
// nothing, and then puts `foo` back.
//
// Not reachable from the property test's generator, which only ever whites out
// a name an *earlier* layer wrote - so it is written by hand, from knowing how
// Φ composes.
func TestAWhiteoutBesideItsFileDeletesIt(t *testing.T) {
	t.Parallel()

	squashed := t.TempDir()
	if err := os.WriteFile(filepath.Join(squashed, "foo"), []byte("resurrected?"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(squashed, ".wh.foo"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(squashed)
	if err != nil {
		t.Fatal(err)
	}

	// What the same range materialises to: nothing at all.
	empty, err := layer.Manifest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if mustFold(t, [][]byte{m}) != mustFold(t, [][]byte{empty}) {
		t.Error("a layer holding both foo and .wh.foo folded to one holding foo," +
			"\n  so a squashed range would resurrect what it deleted")
	}
}
