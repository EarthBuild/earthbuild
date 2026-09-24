package image_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// tree writes a directory to pack.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	for name, body := range files {
		p := filepath.Join(dir, name)
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

// Packing the same directory twice produces the same bytes.
//
// An image's identity is the digest of its layers, so a tar that varies between
// runs is an image that varies between runs - two builds of one input producing
// two different images, and a registry storing both. Directory order, mtimes
// and ownership are the three ways that happens, and all three are normalised.
func TestPackingIsByteReproducible(t *testing.T) {
	t.Parallel()

	dir := tree(t, map[string]string{
		"b.txt":       "second\n",
		testFileA:     "first\n",
		"sub/c.txt":   "third\n",
		"sub/d/e.txt": "fourth\n",
	})

	var first, second bytes.Buffer

	_, _, err := image.Pack(dir, &first)
	if err != nil {
		t.Fatal(err)
	}

	// Touched between the runs: an mtime that reached the tar would show here.
	touched := filepath.Join(dir, testFileA)
	err = os.Chtimes(touched, time.Unix(1, 0), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = image.Pack(dir, &second)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Errorf("two packs of one directory differ: %d and %d bytes",
			first.Len(), second.Len())
	}
}

// The digest names the bytes that were written.
func TestPackReportsTheDigestOfWhatItWrote(t *testing.T) {
	t.Parallel()

	dir := tree(t, map[string]string{testFileA: "content\n"})

	var buf bytes.Buffer

	digest, size, err := image.Pack(dir, &buf)
	if err != nil {
		t.Fatal(err)
	}

	if size != int64(buf.Len()) {
		t.Errorf("reported %d bytes, wrote %d", size, buf.Len())
	}

	if got := image.DigestOf(buf.Bytes()); got != digest {
		t.Errorf("reported %s, the bytes hash to %s", digest, got)
	}
}

// What was packed can be unpacked, and comes back the same.
//
// Round-tripped against this package's own Unpack, which is the reader every
// pulled image already goes through: a tar it cannot read is not a tar.
func TestPackRoundTripsThroughUnpack(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		testFileA:     "first\n",
		"sub/b.txt":   "second\n",
		"sub/c/d.txt": "third\n",
	}

	var buf bytes.Buffer

	_, _, err := image.Pack(tree(t, files), &buf)
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	err = image.Unpack(&buf, out)
	if err != nil {
		t.Fatalf("what Pack wrote, Unpack refused: %v", err)
	}

	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Errorf("%s did not survive the round trip: %v", name, err)

			continue
		}

		if string(got) != want {
			t.Errorf("%s is %q, want %q", name, got, want)
		}
	}
}

// A mode and a symlink survive the round trip.
//
// An image is not a bag of regular files: an executable that arrives without
// its bit is a container that will not start, and a symlink flattened into a
// copy is a base image quietly doubled in size. Both are what tar is *for*, and
// both are easy to lose when normalising everything else away.
func TestPackKeepsModesAndSymlinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// A script this test executes; 0600 cannot run.
	err := os.WriteFile(filepath.Join(dir, "script"), []byte("#!/bin/sh\n"), 0o750) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "target"), []byte("pointed-at\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("target", filepath.Join(dir, "link"))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	_, _, err = image.Pack(dir, &buf)
	if err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	err = image.Unpack(&buf, out)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(out, "script"))
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the executable bit was lost: mode is %v", fi.Mode().Perm())
	}

	li, err := os.Lstat(filepath.Join(out, "link"))
	if err != nil {
		t.Fatal(err)
	}

	if li.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink came back as a regular file")
	}
}
