package image_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/image"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A layer's own unpack can overlap its own fetch.
//
// **The dominant layer is the whole critical path.** With layers kept apart,
// `layers:unpack` on `python:3.13-slim` measured 1.821s against a model of
// arrival(largest) 0.883 + unpack(largest) 0.924 = 1.807 - a fit to 14ms, which
// says nothing else is on that path. Buffered, those two are serial; streamed,
// they are concurrent and the path becomes the longer of them.
//
// E647 measured streaming in the *merged* path and found 3%, which is noise -
// because there the machine is unpacking some other layer while this one
// arrives. Apart, at the tail there is no other layer left to unpack.
func TestAStreamedLayerUnpacksAsItArrives(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{gzipTar(t, "hello", "world")}}
	host := reg.start(t)
	dir := t.TempDir()

	got, _, err := image.PullApart(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true, Stream: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 {
		t.Fatalf("the pull produced %d layers, want 1", len(got))
	}

	body, err := os.ReadFile(filepath.Join(dir, got[0].Dir, "hello"))
	if err != nil {
		t.Fatal(err)
	}

	if string(body) != "world" {
		t.Errorf("the streamed layer holds %q, want %q", body, "world")
	}
}

// TestAStreamedLayerStillHasToMatchItsDigest is why streaming is sound at all.
//
// The bytes are written before they are known to be the right bytes, so the
// check has to happen after the unpack and has to be fatal. It is safe only
// because the caller unpacks into a directory it discards on failure - the
// digest is the whole of the guarantee that a layer is what the manifest said.
func TestAStreamedLayerStillHasToMatchItsDigest(t *testing.T) {
	t.Parallel()

	good := gzipTar(t, "hello", "world")
	reg := &fakeRegistry{layers: [][]byte{good}}

	// Served bytes that are valid gzip and valid tar, and not what was asked
	// for. A blob that merely fails to decompress would be caught by the
	// decompressor and prove nothing about the digest check.
	reg.serveInstead = gzipTar(t, "hello", "not the layer you asked for")

	host := reg.start(t)
	dir := t.TempDir()

	_, _, err := image.PullApart(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true, Stream: true})
	if err == nil {
		t.Fatal("a layer whose bytes do not match its digest was accepted")
	}

	for _, want := range []string{"digest", "sha256:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so a reader cannot tell "+
				"a substituted layer from a network failure:\n  %v", want, err)
		}
	}
}

// TestAStreamedLayerKeepsItsWhiteouts: streaming must not quietly become the
// merged unpacker. Same condition as the buffered path, asked of the other one.
func TestAStreamedLayerKeepsItsWhiteouts(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{layers: [][]byte{
		gzipTar(t, "gone", "here in the base"),
		gzipTar(t, ".wh.gone", ""),
	}}

	host := reg.start(t)
	dir := t.TempDir()

	got, _, err := image.PullApart(context.Background(), host+"/library/test:1", dir,
		image.Options{Plain: true, Stream: true})
	if err != nil {
		t.Fatal(err)
	}

	if !got[1].Marked {
		t.Error("a streamed layer carrying .wh.gone must be reported as marked")
	}

	_, err = os.Stat(filepath.Join(dir, got[1].Dir, ".wh.gone"))
	if err != nil {
		t.Errorf("the streamed whiteout was dropped: %v", err)
	}
}

// TestAnUnpackReportsWhatItHashedOnTheWayIn.
//
// **The bytes are hashed once or twice, and twice is what placing an image
// cost.** `layer.TakeOwnedIn` re-reads the whole tree to digest it - 0.958s of
// a cold `golang:1.26-alpine` pull - over bytes the unpacker had just written.
// It hashes them as it writes and hands the answer on, using the same hasher, so
// what the store is told is what the store would have computed.
func TestAnUnpackReportsWhatItHashedOnTheWayIn(t *testing.T) {
	t.Parallel()

	bodies := map[string]string{"a.txt": "the first file", "b.txt": ""}

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)

	for _, name := range []string{"a.txt", "b.txt"} {
		err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Mode: 0o644,
			Size: int64(len(bodies[name])),
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = tw.Write([]byte(bodies[name]))
		if err != nil {
			t.Fatal(err)
		}
	}

	// A directory and a symlink: neither has content, and neither may appear.
	err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "sub/", Mode: 0o755})
	if err != nil {
		t.Fatal(err)
	}

	err = tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeSymlink, Name: "link", Linkname: "a.txt", Mode: 0o777,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []interface{ Close() error }{tw, zw} {
		cerr := c.Close()
		if cerr != nil {
			t.Fatal(cerr)
		}
	}

	zr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}

	got, err := image.UnpackApart(zr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Digests) != len(bodies) {
		t.Fatalf("the unpack reported %d digests (%v), want one per regular file",
			len(got.Digests), got.Digests)
	}

	for name, body := range bodies {
		h := ir.NewHasher()
		_, _ = h.Write([]byte(body))

		if got.Digests[name] != image.Digest(h.Sum()) {
			t.Errorf("%s reported as %v, want %v - a store told the wrong digest\n"+
				"  files the layer under a name it cannot reproduce (I3)",
				name, got.Digests[name], h.Sum())
		}
	}
}
