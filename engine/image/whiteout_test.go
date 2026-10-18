package image_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// marker builds a layer containing whiteout entries and ordinary files.
func marker(t *testing.T, entries map[string]string, whiteouts ...string) *bytes.Reader {
	t.Helper()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for _, w := range whiteouts {
		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: w, Mode: 0o644, Size: 0,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for name, body := range entries {
		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(body)),
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = tw.Write([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
	}

	err := tw.Close()
	if err != nil {
		t.Fatal(err)
	}

	return bytes.NewReader(buf.Bytes())
}

// A later layer's whiteout deletes what an earlier one wrote.
//
// An image's layers are unpacked into one directory here, so a deletion is a
// deletion: the overlayfs form - a character device 0:0 - describes a layer that
// stays separate and is meaningless in a tree that has already been flattened.
// It also needed CAP_MKNOD, which is why this refused to work anywhere but
// Linux, and `clojure:temurin-8-lein` could not be pulled at all.
func TestAWhiteoutDeletesWhatAnEarlierLayerWrote(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := image.Unpack(marker(t, map[string]string{"etc/keep": "k\n", "etc/gone": "g\n"}), dir)
	if err != nil {
		t.Fatal(err)
	}

	err = image.Unpack(marker(t, nil, "etc/.wh.gone"), dir)
	if err != nil {
		t.Fatalf("a whiteout could not be applied: %v", err)
	}

	_, err = os.Stat(filepath.Join(dir, "etc", "gone"))
	if err == nil {
		t.Error("the deleted file is still there")
	}

	_, err = os.Stat(filepath.Join(dir, "etc", "keep"))
	if err != nil {
		t.Errorf("the whiteout took something it was not aimed at: %v", err)
	}

	// The marker is not a file the image contains.
	_, err = os.Stat(filepath.Join(dir, "etc", ".wh.gone"))
	if err == nil {
		t.Error("the marker itself was unpacked into the image")
	}
}

// A whiteout removes a directory and everything in it.
func TestAWhiteoutDeletesADirectoryWhole(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := image.Unpack(marker(t, map[string]string{"usr/share/doc/a": "a\n"}), dir)
	if err != nil {
		t.Fatal(err)
	}

	err = image.Unpack(marker(t, nil, "usr/share/.wh.doc"), dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "usr", "share", "doc"))
	if err == nil {
		t.Error("the deleted directory is still there")
	}
}

// An opaque marker hides everything a lower layer put in that directory.
func TestAnOpaqueMarkerEmptiesTheDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := image.Unpack(marker(t, map[string]string{"var/lib/old": "o\n"}), dir)
	if err != nil {
		t.Fatal(err)
	}

	err = image.Unpack(marker(t, map[string]string{"var/lib/new": "n\n"}, "var/lib/.wh..wh..opq"), dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(filepath.Join(dir, "var", "lib", "old"))
	if err == nil {
		t.Error("an opaque directory kept what the lower layer put in it")
	}

	_, err = os.Stat(filepath.Join(dir, "var", "lib", "new"))
	if err != nil {
		t.Errorf("the opaque marker took this layer's own file too: %v", err)
	}
}

// A whiteout for something that was never there is not an error.
//
// Layers are built independently and an image may delete a path a different
// base once had. Refusing would fail a pull over an image that is simply
// cautious.
func TestAWhiteoutForNothingIsHarmless(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := image.Unpack(marker(t, nil, "etc/.wh.never-existed"), dir)
	if err != nil {
		t.Errorf("a whiteout for an absent path failed: %v", err)
	}
}
