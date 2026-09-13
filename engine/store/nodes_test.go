package store_test

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A store says which nodes it does not have, so only those are sent.
//
// **This is the subtree skipping, and it is the smaller answer.** StoreHas
// reports what a store holds, which is right for layers because a build asks
// about a handful. A tree has a node per directory and a peer usually holds
// almost all of them, so the useful reply is the few it lacks - that is the set
// the caller acts on, and it is what a sender puts on the wire.
func TestAStoreReportsTheNodesItLacks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)

	tree := treeOfFiles(t, map[string]string{
		"src/main.go":   "one",
		"src/sub/a.go":  "two",
		"vendor/dep.go": "three",
	})

	ids := make([]ir.NodeID, 0, len(tree.Nodes()))
	for d := range tree.Nodes() {
		ids = append(ids, d)
	}

	// Nothing stored: every node is missing.
	if got := st.MissingNodes(ids); len(got) != len(ids) {
		t.Fatalf("an empty store lacks %d of %d nodes, want all", len(got), len(ids))
	}

	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	if got := st.MissingNodes(ids); len(got) != 0 {
		t.Errorf("after storing the tree, %d nodes are still reported missing", len(got))
	}

	// A node from somewhere else is missing, and named.
	other := ir.NodeID{0xaa, 0xbb}

	got := st.MissingNodes(append([]ir.NodeID{other}, ids...))
	if len(got) != 1 || got[0] != other {
		t.Errorf("reported %v missing, want exactly %v", got, other)
	}
}

// Two trees sharing a subtree share its node, so the second sends only what
// differs.
//
// **The payoff, stated as a number.** A base rebuilt with one directory changed
// has every other directory already present, so the transfer is that directory
// and the chain above it rather than the tree.
func TestASecondTreeSendsOnlyWhatChanged(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)

	before := treeOfFiles(t, map[string]string{
		"src/main.go": "one", "src/util.go": "two",
		"docs/a.md": "a", "docs/b.md": "b",
		"vendor/x/dep.go": "dep", "vendor/y/dep.go": "dep2",
	})

	if err := st.NoteNodes(before); err != nil {
		t.Fatal(err)
	}

	after := treeOfFiles(t, map[string]string{
		"src/main.go": "EDITED", "src/util.go": "two",
		"docs/a.md": "a", "docs/b.md": "b",
		"vendor/x/dep.go": "dep", "vendor/y/dep.go": "dep2",
	})

	ids := make([]ir.NodeID, 0, len(after.Nodes()))
	for d := range after.Nodes() {
		ids = append(ids, d)
	}

	missing := st.MissingNodes(ids)

	// `src` changed and the root above it; docs, vendor, vendor/x, vendor/y
	// are untouched and already held.
	if len(missing) != 2 {
		t.Errorf("%d of %d nodes must be sent after a one-file edit, want 2"+
			"\n  the directories the edit did not reach were re-sent, which is"+
			"\n  the whole cost the tree exists to avoid", len(missing), len(ids))
	}
}

// A node handed back verifies against the name it was asked for.
//
// A receiver that trusted the sender would accept bytes under any name, and a
// content-addressed store that does not check is a store of whatever arrived.
func TestAStoredNodeVerifiesAgainstItsName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)

	tree := treeOfFiles(t, map[string]string{"a/b.txt": "one"})

	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	for want := range tree.Nodes() {
		b, err := st.Node(want)
		if err != nil {
			t.Fatalf("node %v: %v", want, err)
		}

		if got := ir.DigestOf(b); got != want {
			t.Errorf("node filed as %v holds bytes naming %v", want, got)
		}
	}
}

// A node whose bytes were corrupted on disk is not handed back.
func TestACorruptNodeIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)

	tree := treeOfFiles(t, map[string]string{"a/b.txt": "one"})

	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	id := tree.Root()

	if err := os.WriteFile(store.NodePath(root, id), []byte("not the node"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := st.Node(id); err == nil {
		t.Error("a node whose bytes do not name it was handed back, so a peer" +
			" would build a tree out of something nobody wrote")
	}
}

// treeOfFiles is the Merkle tree of a one-layer stack holding these files.
func treeOfFiles(t *testing.T, files map[string]string) layer.Tree {
	t.Helper()

	root := t.TempDir()
	writeFiles(t, root, files)

	m, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	f := layer.NewFold()
	if !f.Add(m) {
		t.Fatal("the manifest did not fold")
	}

	return f.Tree()
}
