package store_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// Collecting a layer takes its manifest with it.
//
// **The manifest is a sibling of the layer's directory, not a member of it**, so
// RemoveAll over the directory left it behind: a store pruned to a budget kept
// every manifest it had ever written, for layers it no longer has. Nothing reads
// them - ReadManifest is asked about a layer - so they are bytes that can only
// accumulate.
func TestCollectingALayerTakesItsManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	id := bigLayerIn(t, root, 3, map[string]string{"a/b.txt": "one"})

	if _, err := os.Stat(store.ManifestPath(root, id)); err != nil {
		t.Fatalf("the manifest was not written: %v", err)
	}

	// Collect to nothing: everything goes.
	if _, err := store.Collect(root, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(store.ManifestPath(root, id)); err == nil {
		t.Error("the layer was collected and its manifest was left behind," +
			"\n  so a store pruned to a budget keeps growing by what it prunes")
	}
}

// Collecting removes the nodes nothing left implies.
//
// A node is derived from a manifest, so the live set is the union over the
// manifests that survive. Nodes of a collected layer are referenced by nothing
// and regenerate from nothing, and left alone they grow without bound - the
// store's one directory that only ever gets bigger.
func TestCollectingRemovesUnreferencedNodes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	kept := bigLayerIn(t, root, 40, map[string]string{"keep/a.txt": "one"})
	gone := bigLayerIn(t, root, 3, map[string]string{"drop/b.txt": "two"})

	// Filed explicitly: a capture does not write nodes (see NoteManifest).
	for _, id := range []ir.NodeID{kept, gone} {
		m, ok, err := store.ReadManifest(root, id)
		if err != nil || !ok {
			t.Fatalf("no manifest for %v: %v", id, err)
		}

		f := layer.NewFold()
		if !f.Add(m) {
			t.Fatal("did not fold")
		}

		if err := store.DirStore(root).NoteNodes(f.Tree()); err != nil {
			t.Fatal(err)
		}
	}

	live := nodesOfLayer(t, root, kept)
	dead := nodesOfLayer(t, root, gone)

	if len(store.DirStore(root).MissingNodes(dead)) != 0 {
		t.Fatal("the layer about to be collected had no nodes filed")
	}

	// Keep enough for the big layer and not both. Which one goes is decided
	// rather than left to age: `elsewhere` names the small layer recoverable,
	// and a recoverable layer sorts ahead of an unrecoverable one whatever its
	// age - so the sweep is being tested, not the eviction order.
	if _, err := store.CollectWith(root, 41*1024, func(id ir.NodeID) bool {
		return id == gone
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(store.ManifestPath(root, kept)); err != nil {
		t.Fatalf("the layer meant to survive was collected: %v", err)
	}

	st := store.DirStore(root)

	if got := st.MissingNodes(live); len(got) != 0 {
		t.Errorf("%d of the surviving layer's %d nodes were swept"+
			"\n  a node a live manifest implies is one a peer may still ask for",
			len(got), len(live))
	}

	// `drop/` is only in the collected layer; the root node is shared shape but
	// differs, so at least one node must have gone.
	if got := st.MissingNodes(dead); len(got) == 0 {
		t.Error("every node of the collected layer survived it, so nodes/ only" +
			"\n  ever grows and a pruned store is not pruned")
	}
}

// nodesOfLayer is the node digests a stored layer's manifest implies.
func nodesOfLayer(t *testing.T, root string, id ir.NodeID) []ir.NodeID {
	t.Helper()

	m, ok, err := store.ReadManifest(root, id)
	if err != nil || !ok {
		t.Fatalf("no manifest for %v: %v", id, err)
	}

	f := layer.NewFold()
	if !f.Add(m) {
		t.Fatal("did not fold")
	}

	out := make([]ir.NodeID, 0, len(f.Tree().Nodes()))
	for d := range f.Tree().Nodes() {
		out = append(out, d)
	}

	return out
}

// bigLayerIn stores a layer padded to roughly the given size in bytes.
func bigLayerIn(t *testing.T, root string, kb int, files map[string]string) ir.NodeID {
	t.Helper()

	dir := t.TempDir()
	writeFiles(t, dir, files)

	pad := filepath.Join(dir, "pad.bin")
	if err := os.WriteFile(pad, make([]byte, kb*1024), 0o600); err != nil {
		t.Fatal(err)
	}

	took, err := layer.Take(dir)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(store.LayerStore(root).Path(took.ID), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(store.LayerStore(root).Path(took.ID), "pad.bin"),
		make([]byte, kb*1024), 0o600); err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(root, took.ID, m)

	return took.ID
}

// What the node sweep costs, as a function of how many layers a store holds.
//
// It refolds every surviving manifest, so the cost is the store's size and not
// what was collected. Reported rather than asserted: Collect is what a person
// runs to get space back, and the number says whether that is still true.
func BenchmarkSweepOnCollect(b *testing.B) {
	for _, layers := range []int{50, 200} {
		root := b.TempDir()

		for i := range layers {
			dir := b.TempDir()

			for j := range 50 {
				at := filepath.Join(dir, fmt.Sprintf("d%d/f%d.txt", j%5, j))
				if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
					b.Fatal(err)
				}

				if err := os.WriteFile(at, []byte{byte(i), byte(j)}, 0o600); err != nil {
					b.Fatal(err)
				}
			}

			took, err := layer.Take(dir)
			if err != nil {
				b.Fatal(err)
			}

			m, err := layer.Manifest(dir)
			if err != nil {
				b.Fatal(err)
			}

			if err := os.MkdirAll(store.LayerStore(root).Path(took.ID), 0o750); err != nil {
				b.Fatal(err)
			}

			store.NoteManifest(root, took.ID, m)
		}

		b.Run(fmt.Sprintf("layers=%d", layers), func(b *testing.B) {
			for b.Loop() {
				if _, err := store.Collect(root, 1<<40); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// What noting the nodes adds to noting a manifest.
//
// On the build's path, once per layer captured. The manifest's own justification
// is that it costs "a tenth of a percent of the layer" against a walk that has
// already happened; this has to stand beside that number, not beside zero.
func BenchmarkNoteManifest(b *testing.B) {
	dir := b.TempDir()

	for j := range 4000 {
		at := filepath.Join(dir, fmt.Sprintf("d%02d/s%02d/f%d.txt", j%20, (j/20)%10, j))
		if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
			b.Fatal(err)
		}

		if err := os.WriteFile(at, []byte("body"), 0o600); err != nil {
			b.Fatal(err)
		}
	}

	m, err := layer.Manifest(dir)
	if err != nil {
		b.Fatal(err)
	}

	took, err := layer.Take(dir)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("note", func(b *testing.B) {
		for b.Loop() {
			root := b.TempDir()
			if err := os.MkdirAll(store.LayerStore(root).Path(took.ID), 0o750); err != nil {
				b.Fatal(err)
			}

			store.NoteManifest(root, took.ID, m)
		}
	})

	b.Run("walk-that-produced-it", func(b *testing.B) {
		for b.Loop() {
			if _, err := layer.Manifest(dir); err != nil {
				b.Fatal(err)
			}
		}
	})
}
