package image_test

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// An unpacked symlink carries the time the archive gave it.
//
// **A symlink has an mtime of its own**, and a layer's identity covers it
// (green paper §3.3). The unpacker set mode, ownership and time for every entry
// except links, on the reasoning that all three apply to what a link points at
// rather than to the link - true of `os.Chmod` and `os.Chtimes`, and the wrong
// conclusion: `Lchtimes` sets a link's own time without following it, which is
// what `layer/unpack.go` has always done.
//
// The cost was that no two machines could agree about a base image. Alpine
// carries 335 symlinks and every one of them differed between two unpacks of
// the same bytes, so the placed layer had a different identity on every machine
// - which makes an L2 hit across machines impossible and would have looked, in
// the fleet, like a transfer that corrupted something (E546).
func TestAnUnpackedSymlinkCarriesItsArchivedTime(t *testing.T) {
	t.Parallel()

	when := time.Unix(1_600_000_000, 0)

	stream := tarOf(t,
		file("busybox", "elf", 0o755),
		link("arch", "busybox", when),
	)

	dir := t.TempDir()

	err := image.Unpack(stream, dir)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(filepath.Join(dir, "arch"))
	if err != nil {
		t.Fatal(err)
	}

	if !fi.ModTime().Equal(when) {
		t.Errorf("the unpacked link carries %v and the archive said %v"+
			"\n  a link's mtime is part of the layer's identity, so an unpack"+
			"\n  that stamps it with now names the same image differently on"+
			"\n  every machine", fi.ModTime().UTC(), when.UTC())
	}
}

// link is a symlink entry carrying its own modification time.
func link(name, target string, when time.Time) func(*tar.Writer) {
	return func(w *tar.Writer) {
		_ = w.WriteHeader(&tar.Header{
			Typeflag: tar.TypeSymlink, Name: name, Linkname: target,
			Mode: 0o777, ModTime: when,
		})
	}
}
