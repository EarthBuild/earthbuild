package store

import (
	"os"
	"path/filepath"
	"testing"
)

// The hash is the same in every process, or a saved index is nonsense.
//
// **maphash.MakeSeed is per-process.** An index written with one seed and read
// by the next build says a layer holds nothing it holds, and "holds nothing" is
// the answer that loses files. A saved index needs a hash that is a function of
// the path and of nothing else.
func TestThePathHashIsTheSameInEveryProcess(t *testing.T) {
	t.Parallel()

	// Written down rather than computed: a test that recomputes the thing it
	// checks agrees with any change, including the one that breaks every index
	// already on disk.
	for path, want := range map[string]uint64{
		"usr/bin/cat": 0x646c24f1645d021c,
		"":            0xcbf29ce484222325,
	} {
		if got := hashPath(path); got != want {
			t.Errorf("hashPath(%q) = %#x, want %#x"+
				"\n  if this changed on purpose, every saved index is stale and"+
				" the format version has to change with it", path, got, want)
		}
	}
}

// An index survives the process that built it.
func TestASavedIndexIsReadBack(t *testing.T) {
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

	built := walkLayer(root)
	if built == nil {
		t.Fatal("the layer did not index")
	}

	at := filepath.Join(t.TempDir(), "layer.index")

	err = saveIndex(at, built)
	if err != nil {
		t.Fatal(err)
	}

	back := loadIndex(at)
	if back == nil {
		t.Fatal("a saved index did not read back")
	}

	if !back.mayHave("usr/bin/cat") {
		t.Error("a path the layer holds is absent from the index that was saved")
	}

	if back.mayHave("usr/bin/grep") {
		t.Error("a path the layer does not hold is present in the saved index")
	}
}

// A file that is not a whole index is no index.
//
// **Truncation is the dangerous corruption.** A short read gives a valid-looking
// smaller set, which answers "absent" for everything that was cut off - and
// absent is the answer that makes a file vanish from a base and a cache entry
// look fresh when it is not. The count is written down and checked.
func TestAnIndexThatIsNotWholeIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	at := filepath.Join(dir, "layer.index")

	idx := &layerIndex{has: map[uint64]struct{}{}}
	for i := range 50 {
		idx.has[uint64(i)] = struct{}{}
	}

	err := saveIndex(at, idx)
	if err != nil {
		t.Fatal(err)
	}

	whole, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}

	for _, cut := range []int{1, len(whole) / 2, len(whole) - 1} {
		short := filepath.Join(dir, "short.index")

		err = os.WriteFile(short, whole[:cut], 0o600)
		if err != nil {
			t.Fatal(err)
		}

		if loadIndex(short) != nil {
			t.Errorf("an index truncated to %d of %d bytes was accepted;"+
				" it would answer 'absent' for everything cut off", cut, len(whole))
		}
	}
}

// Something that is not an index at all is no index.
func TestRubbishIsNotAnIndex(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "layer.index")

	err := os.WriteFile(at, []byte("this is not an index, it is a poem"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if loadIndex(at) != nil {
		t.Error("a file that is not an index was read as one")
	}
}

// An absent index is absent, not empty.
func TestAnAbsentIndexIsNil(t *testing.T) {
	t.Parallel()

	if loadIndex(filepath.Join(t.TempDir(), "nope.index")) != nil {
		t.Error("a missing index read as an empty one, which answers 'absent'" +
			" for every path in the layer")
	}
}

// A layer's index goes when the layer does.
//
// **Derived, so an orphan is litter rather than a fault** - the identity is the
// content, so an index left behind would still be right if the same layer ever
// returned. But it grows without bound in a store that collects, and a file
// nobody will ever open again is the kind of thing a store fills up with.
func TestCollectingALayerTakesItsIndex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layers := filepath.Join(root, "layers")

	err := os.MkdirAll(filepath.Join(layers, "abc"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(layers, "abc") + indexSuffix

	err = os.WriteFile(at, []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	forgetLayerIndex(filepath.Join(layers, "abc"))

	if _, err = os.Stat(at); err == nil {
		t.Error("the index outlived the layer it describes")
	}
}
