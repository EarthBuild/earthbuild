package image

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A layer of the store keeps the times the store holds.
//
// **An image that flattens them is an image no incremental tool can use.** A
// published build tree is only worth publishing if the next build can stand on
// it, and cargo decides what to recompile by comparing each source's mtime
// against the artefact in `target/`. Packed at a fixed epoch, every artefact
// claims 1970, every source arrives newer, and the whole tree recompiles -
// which is the entire value of carrying it, spent.
//
// **Still reproducible.** A store layer's mtimes are part of its identity (I8),
// so two machines materialising the same layer hold the same times and pack the
// same bytes. Normalising them here bought nothing that the layer's own
// content-addressing had not already bought.
func TestAStoredLayerKeepsItsTimes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	at := filepath.Join(dir, "artefact.rlib")

	err := os.WriteFile(at, []byte("compiled"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Nanoseconds, because that resolution is the difference between two edits
	// inside one tick being one change and being two.
	when := time.Unix(1_700_000_000, 123_456_789)

	err = os.Chtimes(at, when, when)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	_, _, err = PackStored(dir, &buf)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	got := modTimesIn(t, &buf)["artefact.rlib"]
	if !got.Equal(when) {
		t.Errorf("the layer carries %v, wanted %v", got, when)
	}
}

// The staged context still normalises: those mtimes are the host's, taken from
// whenever a copy happened, and two machines never agree on them.
func TestAStagedTreeIsStillFlattened(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	_, _, err = Pack(dir, &buf)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}

	for name, when := range modTimesIn(t, &buf) {
		if !when.Equal(epoch) {
			t.Errorf("%s carries %v, wanted the epoch", name, when)
		}
	}
}

// And a stored layer is still byte-reproducible: the same tree packs the same
// bytes twice, which is what an image's identity rests on.
func TestAStoredLayerPacksTheSameBytesTwice(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, n := range []string{"b", "a", "c"} {
		err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	var first, second bytes.Buffer

	d1, _, err := PackStored(dir, &first)
	if err != nil {
		t.Fatal(err)
	}

	d2, _, err := PackStored(dir, &second)
	if err != nil {
		t.Fatal(err)
	}

	if d1 != d2 || !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Errorf("two packs of one tree differ: %s and %s", d1, d2)
	}

	// Sorted, so the order cannot come from the filesystem's listing.
	var names []string
	for r := tar.NewReader(&first); ; {
		h, err := r.Next()
		if err != nil {
			break
		}

		names = append(names, h.Name)
	}

	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("entries are not sorted: %v", names)

			break
		}
	}
}
