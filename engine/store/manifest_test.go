package store_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// A manifest written beside a layer comes back.
func TestAManifestIsKeptBesideItsLayer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.MkdirAll(filepath.Join(dir, "layers"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	id := ir.NodeID{1, 2, 3}
	want := []byte("a manifest, encoded")

	store.NoteManifest(dir, id, want)

	got, kept, err := store.ReadManifest(dir, id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if !kept {
		t.Fatal("the manifest was not kept")
	}

	if !bytes.Equal(got, want) {
		t.Errorf("read back %q", got)
	}
}

// Absent is an answer, not a failure: a layer stored before manifests existed
// has none, and every reader has to be able to walk instead.
func TestAMissingManifestIsNotAnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, kept, err := store.ReadManifest(dir, ir.NodeID{9})
	if err != nil {
		t.Errorf("an absent manifest was an error: %v", err)
	}

	if kept {
		t.Error("a manifest nobody wrote was reported as kept")
	}
}

// Nothing to say, nothing written - an empty manifest would attest to an empty
// layer, which is a claim rather than an absence.
func TestAnEmptyManifestIsNotWritten(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.MkdirAll(filepath.Join(dir, "layers"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(dir, ir.NodeID{4}, nil)

	if _, kept, _ := store.ReadManifest(dir, ir.NodeID{4}); kept {
		t.Error("an empty manifest was written")
	}
}

// **Every layer the store files gets a manifest.** A capture has already walked
// the tree, so writing down what it found costs an encode; a named tree has not
// been walked, and one parallel pass over a tree that was just written is worth
// never reading it again. Either way the next reader - a copy deciding whether a
// file changed, a peer checking a fragment - is told rather than made to look.
func TestAPlacedLayerIsGivenAManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st := store.DirStore(dir)

	staging, err := st.Staging(".t-")
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(staging, "a.txt"), []byte("hello"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	id, err := st.Place(staging)
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	m, kept, err := store.ReadManifest(dir, id)
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}

	if !kept {
		t.Fatal("a placed layer has no manifest beside it")
	}

	files, err := layer.Files(m)
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}

	if got := files["a.txt"].Size; got != int64(len("hello")) {
		t.Errorf("the manifest says a.txt is %d bytes, it is %d", got, len("hello"))
	}
}

// A tree filed under a name the caller gave - a build context, whose identity
// is the plan's - is manifested too, and that is the one worth having: it is
// what every `COPY` in the build reads from.
func TestANamedLayerIsGivenAManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	st := store.DirStore(dir)

	staging, err := st.Staging(".t-")
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(staging, "a.txt"), []byte("hello"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var id ir.NodeID

	id[0] = 9

	err = st.PutNamed(id, staging)
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	m, kept, err := store.ReadManifest(dir, id)
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}

	if !kept {
		t.Fatal("a named layer has no manifest beside it")
	}

	files, err := layer.Files(m)
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}

	if _, ok := files["a.txt"]; !ok {
		t.Error("the manifest does not mention the only file in the layer")
	}
}
