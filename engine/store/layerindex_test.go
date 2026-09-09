package store

import (
	"os"
	"path/filepath"
	"testing"
)

// A layer says which paths it cannot have, so a stack stops stat-ing for them.
//
// **Because a lookup walks every layer.** stackView.Digest probes each layer in
// turn until one answers, so a path living in the base image is looked for in
// all the layers above it first - two syscalls each, nearly all of them
// ENOENT. With 6299 observed paths over a stack at the 64-layer flatten
// threshold that is some 800,000 syscalls, and it measured 4.0s inside a guest
// against 1ms on a host with a warm dentry cache.
//
// A layer is immutable, so what it contains can be learned once and remembered
// for ever. Existence only: the digest still comes from the file, so nothing
// about what a layer means changes here.
func TestALayerSaysWhatItCannotHave(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "usr", "bin"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "usr", "bin", "cat"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	idx := indexOfLayer(root)
	if idx == nil {
		t.Fatal("a layer that exists has no index")
	}

	if !idx.mayHave("usr/bin/cat") {
		t.Error("a path the layer holds is reported absent, which would lose it")
	}

	if idx.mayHave("usr/bin/grep") {
		t.Error("a path the layer does not hold is reported present;" +
			" that is only a wasted stat, but it is the whole saving")
	}
}

// The index is remembered, because a layer cannot change.
func TestALayerIsIndexedOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, "a"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	first := indexOfLayer(root)

	second := indexOfLayer(root)
	if first != second {
		t.Error("a layer was indexed twice; layers are immutable and the walk" +
			" is the expensive half")
	}
}

// A directory that is not there indexes to nothing, and says so rather than
// pretending the layer is empty - an empty index would answer "absent" for
// every path and quietly lose files.
func TestAMissingLayerHasNoIndex(t *testing.T) {
	t.Parallel()

	if idx := indexOfLayer(filepath.Join(t.TempDir(), "nope")); idx != nil {
		t.Error("a layer that is not there produced an index, which would" +
			" answer 'absent' for every path in it")
	}
}
