package store_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
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
