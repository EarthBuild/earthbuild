package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A guest reports the tree nodes its store lacks, and only those.
//
// **The store is the guest's**, so which subtrees it already holds is a question
// only the guest can answer - the same move KindStoreHas made for layers, at the
// granularity that lets a sender skip what it need not send.
func TestAGuestReportsTheTreeNodesItLacks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	st := store.DirStore(root)

	tree := treeOfDir(t, map[string]string{
		"src/main.go": "one", "docs/a.md": "a", "vendor/dep.go": "dep",
	})

	ids := make([]ir.NodeID, 0, len(tree.Nodes()))
	for d := range tree.Nodes() {
		ids = append(ids, d)
	}

	c := pairWith(t, &guest.Server{LayerDir: root})

	ctx := context.Background()

	missing, err := c.TreeMissing(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}

	if len(missing) != len(ids) {
		t.Fatalf("an empty store lacks %d of %d nodes, want all", len(missing), len(ids))
	}

	if err := st.NoteNodes(tree); err != nil {
		t.Fatal(err)
	}

	missing, err = c.TreeMissing(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}

	if len(missing) != 0 {
		t.Errorf("after storing the tree the guest still lacks %d nodes, so a"+
			"\n  sender would ship subtrees the peer already has", len(missing))
	}
}

// treeOfDir is the Merkle tree of a one-layer stack holding these files.
func treeOfDir(t *testing.T, files map[string]string) layer.Tree {
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

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	f := layer.NewFold()
	if !f.Add(m) {
		t.Fatal("the manifest did not fold")
	}

	return f.Tree()
}
