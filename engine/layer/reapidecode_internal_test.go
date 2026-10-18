package layer

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What we write, we can read back.
//
// **A walk needs only the children.** Fetching a tree means asking for the root,
// finding what it names, and asking for whatever is missing - so the decoder
// this engine needs is not a protobuf library, it is "which digests does this
// Directory point at". Everything else in the message is for whoever
// materialises it.
func TestChildDigestsRoundTrip(t *testing.T) {
	t.Parallel()

	f := NewFold()

	for _, p := range []string{
		"top.txt", "alpha/one.txt", "beta/two.txt", "beta/deep/three.txt",
	} {
		e := entry{path: p, mode: 0o644, size: 1}
		copy(e.content[:], p)
		f.merged[p] = e
		f.resync(p)
	}

	tree := f.Tree()

	// Every node's children must be nodes of this tree, and every node but the
	// root must be somebody's child.
	claimed := map[ir.NodeID]bool{}

	for id, b := range tree.Nodes() {
		kids, err := ChildDigests(b)
		if err != nil {
			t.Fatalf("node %v: %v", id, err)
		}

		for _, k := range kids {
			if _, ok := tree.Nodes()[k]; !ok {
				t.Errorf("node %v names a child %v that is not in the tree", id, k)
			}

			claimed[k] = true
		}
	}

	for id := range tree.Nodes() {
		if id != tree.Root() && !claimed[id] {
			t.Errorf("node %v is in the tree and nothing points at it", id)
		}
	}

	if claimed[tree.Root()] {
		t.Error("something points at the root, which has no parent")
	}

	// alpha, beta, beta/deep and the root.
	if len(tree.Nodes()) != 4 {
		t.Errorf("%d nodes, want 4", len(tree.Nodes()))
	}
}

// Bytes that are not a Directory are refused rather than half-read.
//
// A peer sends these, so "unreadable" has to be an answer. Protobuf is
// permissive by design - unknown fields are skipped - so the check that matters
// is that a length runs past the end of the buffer, which is what a truncated
// or corrupt message looks like.
func TestBytesThatAreNotADirectoryAreRefused(t *testing.T) {
	t.Parallel()

	for _, b := range [][]byte{
		{0x12, 0x7f},             // a DirectoryNode claiming 127 bytes that are not there
		{0x12, 0x02, 0x12, 0x40}, // a Digest claiming 64 bytes that are not there
		{0xff},                   // a tag with no field
	} {
		if _, err := ChildDigests(b); err == nil {
			t.Errorf("%x was read as a Directory", b)
		}
	}
}

// An empty Directory has no children and is not an error.
func TestAnEmptyDirectoryHasNoChildren(t *testing.T) {
	t.Parallel()

	kids, err := ChildDigests(nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(kids) != 0 {
		t.Errorf("%d children in an empty directory", len(kids))
	}
}
