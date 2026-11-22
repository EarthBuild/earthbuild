package layer_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// treeOf folds a stack and returns its Merkle tree.
func merkleOf(t *testing.T, ms ...[]byte) layer.Tree {
	t.Helper()

	f := layer.NewFold()

	for _, m := range ms {
		if !f.Add(m) {
			t.Fatal("a manifest this test wrote could not be folded")
		}
	}

	return f.Tree()
}

// A subtree digests to the same thing wherever it sits.
//
// **This is the property the whole tier rests on.** A node names what is under
// it and nothing about where it is, so a directory that two bases share is one
// node in both - which is what lets a peer answer "I have that subtree already"
// without being told what surrounds it. A digest that folded the path in would
// make every identical vendor directory a different blob.
func TestASubtreeIsTheSameWhereverItSits(t *testing.T) {
	t.Parallel()

	here := merkleOf(t, manifestOf(t, map[string]string{
		"vendor/pkg/a.go": "package a",
		"vendor/pkg/b.go": "package b",
	}))

	there := merkleOf(t, manifestOf(t, map[string]string{
		"third_party/pkg/a.go": "package a",
		"third_party/pkg/b.go": "package b",
	}))

	if here.Root() == there.Root() {
		t.Fatal("two trees differing in a top-level name share a root")
	}

	shared := 0

	for d := range here.Nodes() {
		if _, ok := there.Nodes()[d]; ok {
			shared++
		}
	}

	// `pkg` and its contents are identical; only the name above it differs.
	if shared == 0 {
		t.Error("two trees holding an identical subtree share no node, so a" +
			"\n  peer cannot skip what it already has")
	}
}

// Changing one file leaves every subtree that does not contain it alone.
//
// **This is what makes the digest incremental and the fetch lazy.** A step that
// writes one file must not invalidate the nodes of directories it never
// touched, or the answer to "what do you still need" is always "everything".
func TestOneChangedFileLeavesItsSiblingsAlone(t *testing.T) {
	t.Parallel()

	before := merkleOf(t, manifestOf(t, map[string]string{
		"src/main.go":   "package main",
		"src/util.go":   "package main",
		"docs/read.md":  "hello",
		"docs/more.md":  "world",
		"vendor/dep.go": "package dep",
	}))

	after := merkleOf(t, manifestOf(t, map[string]string{
		"src/main.go":   "package main // edited",
		"src/util.go":   "package main",
		"docs/read.md":  "hello",
		"docs/more.md":  "world",
		"vendor/dep.go": "package dep",
	}))

	if before.Root() == after.Root() {
		t.Fatal("a changed file did not change the root")
	}

	// Everything the change did not reach must still be held in common.
	kept := 0

	for d := range before.Nodes() {
		if _, ok := after.Nodes()[d]; ok {
			kept++
		}
	}

	if kept < 2 {
		t.Errorf("only %d nodes survived a one-file edit, of %d"+
			"\n  the untouched directories were re-digested, so nothing can be"+
			"\n  skipped and the tree is a flat digest wearing a tree's name",
			kept, len(before.Nodes()))
	}
}

// Every node the tree names is a blob the tree can hand over.
//
// A digest nobody can produce the bytes for is not addressable, and the whole
// point of naming subtrees is that a peer can ask for one by name.
func TestEveryNodeIsAddressable(t *testing.T) {
	t.Parallel()

	tr := merkleOf(t, manifestOf(t, map[string]string{
		"a/b/c.txt": "deep",
		"a/d.txt":   "shallow",
		"e.txt":     "top",
	}))

	nodes := tr.Nodes()

	if _, ok := nodes[tr.Root()]; !ok {
		t.Fatal("the root is not among the nodes, so a peer given the root" +
			" digest cannot ask for its bytes")
	}

	// a, a/b and the root: three directories, three nodes.
	if len(nodes) != 3 {
		t.Errorf("a tree with two nested directories has %d nodes, want 3", len(nodes))
	}

	for d, b := range nodes {
		if len(b) == 0 {
			t.Errorf("node %v has no bytes", d)
		}
	}
}

// The root of the tree is 𝜏, which is what Κₜ keys on.
func TestTheRootIsTheTreeDigest(t *testing.T) {
	t.Parallel()

	m := manifestOf(t, map[string]string{"a.txt": "one", "d/b.txt": "two"})

	flat, ok := layer.TreeFromManifests([][]byte{m})
	if !ok {
		t.Fatal("the manifest did not fold")
	}

	if got := merkleOf(t, m).Root(); got != flat {
		t.Errorf("TreeFromManifests gave %v and the tree's root is %v"+
			"\n  two definitions of 𝜏 is two answers to what a base is", flat, got)
	}
}

var _ = ir.NodeID{}

// A carried fold names the same tree a fresh one would.
//
// **This is what the incremental digest rests on.** A node keeps the name it was
// last given and a layer dirties only what it moved, so a directory missed by
// the dirtying is a directory named by what it used to hold - and the key would
// be a base that no longer exists. A stale node is a false hit (I3), not a
// stale number.
func TestACarriedFoldAgreesWithAFreshOne(t *testing.T) {
	t.Parallel()

	ms := [][]byte{
		manifestOf(t, map[string]string{
			"src/main.go": "one", "src/util.go": "two",
			"docs/a.md": "a", "docs/b.md": "b", "vendor/dep.go": "dep",
		}),
		manifestOf(t, map[string]string{"src/main.go": "edited"}),
		manifestOf(t, map[string]string{"docs/.wh.a.md": ""}),
		manifestOf(t, map[string]string{"vendor/.wh..wh..opq": "", "vendor/new.go": "new"}),
		manifestOf(t, map[string]string{"src/.wh.util.go": "", "extra/c.txt": "c"}),
		manifestOf(t, map[string]string{"docs/b.md": "rewritten"}),
	}

	carried := layer.NewFold()

	for i, m := range ms {
		if !carried.Add(m) {
			t.Fatalf("layer %d did not fold", i)
		}

		// A fold built from nothing over the same prefix, every step of the way.
		fresh, ok := layer.TreeFromManifests(ms[:i+1])
		if !ok {
			t.Fatalf("the prefix to %d did not fold", i)
		}

		if got := carried.Digest(); got != fresh {
			t.Fatalf("after layer %d the carried fold names %v and a fresh one %v"+
				"\n  a directory the layer moved kept the name it had before, so"+
				"\n  the key is a base that no longer exists", i, got, fresh)
		}
	}
}

// The addressable tree and the cached digest name the same root.
//
// Two ways to compute 𝜏 is two answers to what a base is, and the one that
// ships blobs must agree with the one that makes keys or a peer fetches a
// subtree nobody asked for.
func TestShippingAndKeyingAgree(t *testing.T) {
	t.Parallel()

	f := layer.NewFold()

	for _, m := range [][]byte{
		manifestOf(t, map[string]string{"a/b.txt": "one", "c.txt": "two"}),
		manifestOf(t, map[string]string{"a/d.txt": "three"}),
		manifestOf(t, map[string]string{"a/.wh.b.txt": ""}),
	} {
		if !f.Add(m) {
			t.Fatal("a manifest did not fold")
		}
	}

	if f.Digest() != f.Tree().Root() {
		t.Errorf("Digest gave %v and Tree gave %v", f.Digest(), f.Tree().Root())
	}
}
