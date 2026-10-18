package store_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A folder extending the stack it just folded agrees with folding from scratch.
//
// **Correctness first: the memo may not change the answer.** Κₜ keys on the
// fold, so a rolling fold that drifted from a fresh one by a single entry would
// serve a step the result of a step over a different filesystem.
func TestARollingFoldAgreesWithAFreshOne(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)
	f := store.NewFolder(root)

	var stack []ir.NodeID

	// A ladder with the shapes the fold has to get right: an overwrite, a
	// whiteout, and an opaque marker.
	for i, files := range []map[string]string{
		{"a.txt": "one", "dir/b.txt": "two", "dir/c.txt": "three"},
		{"a.txt": "overwritten"},
		{"dir/.wh.b.txt": ""},
		{"dir/.wh..wh..opq": "", "dir/d.txt": "four"},
		{"e.txt": "five"},
	} {
		stack = append(stack, layerWithManifest(t, root, files))

		want, okWant := st.TreeOf(stack)
		got, okGot := f.TreeOf(stack)

		if okWant != okGot || want != got {
			t.Fatalf("at depth %d the rolling fold gave %v/%v and a fresh one"+
				"\n  %v/%v - the memo changed the answer", i+1, got, okGot, want, okWant)
		}
	}
}

// A folder extending its own prefix does not re-read what it already folded.
//
// **The probe is the base's manifest, removed.** A fold that starts over reads
// every manifest again and silently skips the one that is gone - producing a
// tree of the layers above it. A fold that extends what it held never looks, so
// the answer is unchanged. Nothing observes the read directly; this observes
// what a re-read would cost.
func TestAFolderDoesNotRefoldThePrefixItHolds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	f := store.NewFolder(root)

	base := layerWithManifest(t, root, map[string]string{"base.txt": "deps"})
	above := layerWithManifest(t, root, map[string]string{"above.txt": "step"})

	stack := []ir.NodeID{base, above}

	want, ok := store.DirStore(root).TreeOf(stack)
	if !ok {
		t.Fatal("the ladder did not fold")
	}

	// Fold the prefix, so the folder holds it.
	if _, ok := f.TreeOf([]ir.NodeID{base}); !ok {
		t.Fatal("the base did not fold")
	}

	// Now make re-reading it impossible to do silently.
	if err := os.Remove(store.ManifestPath(root, base)); err != nil {
		t.Fatal(err)
	}

	got, ok := f.TreeOf(stack)
	if !ok {
		t.Fatal("extending a held prefix failed")
	}

	if got != want {
		t.Errorf("extending gave %v, folding from scratch gave %v"+
			"\n  the prefix was re-read rather than reused, so every step above a"+
			"\n  base pays for that base again", got, want)
	}
}

// A stack that is not an extension is folded from scratch, and correctly.
func TestAFolderThatCannotExtendStartsOver(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)
	f := store.NewFolder(root)

	one := layerWithManifest(t, root, map[string]string{"a.txt": "one"})
	two := layerWithManifest(t, root, map[string]string{"b.txt": "two"})
	three := layerWithManifest(t, root, map[string]string{"c.txt": "three"})

	// Two branches over one base, asked alternately - a DAG, not a chain.
	for i, stack := range [][]ir.NodeID{
		{one, two}, {one, three}, {one, two}, {two, three}, {one},
	} {
		want, okWant := st.TreeOf(stack)

		got, okGot := f.TreeOf(stack)
		if okWant != okGot || want != got {
			t.Errorf("branch %d: rolling %v/%v, fresh %v/%v", i, got, okGot, want, okWant)
		}
	}
}

// A corrupt manifest reached while extending does not poison the next answer.
//
// **A partial fold is not a prefix of anything.** Add applies entries as it goes,
// so a manifest that fails to decode leaves the rolling state holding some of
// that layer - and keeping it would make the next extension a fold over a stack
// that never existed.
func TestACorruptManifestDoesNotPoisonTheNextFold(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)
	f := store.NewFolder(root)

	base := layerWithManifest(t, root, map[string]string{"a.txt": "one"})

	corrupt := ir.NodeID{0xba, 0xdd}
	if err := os.MkdirAll(fmt.Sprintf("%s/layers", root), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(store.ManifestPath(root, corrupt), []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := f.TreeOf([]ir.NodeID{base}); !ok {
		t.Fatal("the base did not fold")
	}

	if _, ok := f.TreeOf([]ir.NodeID{base, corrupt}); ok {
		t.Fatal("a stack holding a corrupt manifest folded")
	}

	// The folder is asked again about a stack it could answer before.
	want, _ := st.TreeOf([]ir.NodeID{base})

	got, ok := f.TreeOf([]ir.NodeID{base})
	if !ok || got != want {
		t.Errorf("after a corrupt layer the base folded to %v/%v, want %v"+
			"\n  the failed extension was kept and every later answer is over a"+
			"\n  stack that never existed", got, ok, want)
	}
}

// Independent chains each keep their prefix.
//
// **The scheduler claims the cache before it takes a slot**, so Κₜ derivations
// from parallel branches interleave: chain A, chain B, chain A again. A folder
// holding one prefix has it thrown away by every neighbour, and measured on two
// chains that is the whole of the saving - 9.3ms a fold became 15.7ms, which is
// what folding from scratch cost. Holding one prefix per chain is what makes the
// memo survive the concurrency the engine actually has.
//
// The probe is each chain's own manifest, removed once that chain is held: a
// folder that kept the prefix never looks at it again, and one that started over
// silently skips it and folds a tree missing its base.
func TestChainsDoNotEvictEachOther(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	f := store.NewFolder(root)

	shared := layerWithManifest(t, root, map[string]string{"base.txt": "deps"})

	chains := make([][]ir.NodeID, 0, 3)

	for _, name := range []string{"a", "b", "c"} {
		tip := layerWithManifest(t, root, map[string]string{name + ".txt": name})
		above := layerWithManifest(t, root, map[string]string{name + "-up.txt": "x"})
		chains = append(chains, []ir.NodeID{shared, tip, above})
	}

	// Hold every chain's own prefix, interleaved as the scheduler would.
	for i, c := range chains {
		if _, ok := f.TreeOf(c[:2]); !ok {
			t.Fatalf("chain %d did not fold", i)
		}
	}

	// Now re-reading the shared base is impossible to do silently.
	if err := os.Remove(store.ManifestPath(root, shared)); err != nil {
		t.Fatal(err)
	}

	// Every chain extends what it held. Collected before anything else is
	// asked of the folder, so the measurement does not evict what it measures.
	got := make([]ir.NodeID, len(chains))

	for i, c := range chains {
		var ok bool
		if got[i], ok = f.TreeOf(c); !ok {
			t.Fatalf("chain %d could not be extended", i)
		}
	}

	// A fold that lost the prefix skips the base it can no longer read, so it
	// lands on the tree of the layers above it alone.
	for i, c := range chains {
		bare, ok := f.TreeOf(c[1:])
		if !ok {
			t.Fatalf("chain %d without its base did not fold", i)
		}

		if got[i] == bare {
			t.Errorf("chain %d folded to the same tree with and without its base"+
				"\n  its prefix was evicted by another chain, so every step of a"+
				"\n  parallel build pays for that base again", i)
		}
	}
}
