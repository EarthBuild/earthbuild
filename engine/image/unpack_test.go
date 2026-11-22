package image_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/image"
)

// tarOf builds a tar stream from entries, so each test states exactly the
// bytes it is unpacking.
func tarOf(t *testing.T, entries ...func(*tar.Writer)) *bytes.Reader {
	t.Helper()

	var buf bytes.Buffer

	w := tar.NewWriter(&buf)
	for _, e := range entries {
		e(w)
	}

	err := w.Close()
	if err != nil {
		t.Fatal(err)
	}

	return bytes.NewReader(buf.Bytes())
}

func file(name, body string, mode int64) func(*tar.Writer) {
	return func(w *tar.Writer) {
		_ = w.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Mode: mode, Size: int64(len(body)),
		})
		_, _ = w.Write([]byte(body))
	}
}

// A registry serves bytes from anyone. An entry naming a path outside the layer
// must be refused, not written: `../../etc/passwd` in a pulled image is an
// attempt to overwrite the host, and unpacking it is remote code execution at
// the next boot.
func TestPathTraversalIsRefused(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"../escape",
		"../../etc/passwd",
		"a/../../escape",
		"/absolute",
		"./../escape",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()

			err := image.Unpack(tarOf(t, file(name, "owned", 0o644)), root)
			if err == nil {
				t.Fatalf("unpacking %q was permitted", name)
			}

			// The refusal must name the entry: an image with one bad path among
			// nine thousand is otherwise undebuggable.
			if !bytes.Contains([]byte(err.Error()), []byte(name)) {
				t.Errorf("refusal does not name the offending entry:\n%s", err)
			}

			// And nothing may have been written outside the root.
			_, err = os.Stat(filepath.Join(filepath.Dir(root), "escape"))
			if err == nil {
				t.Error("a file was written outside the unpack root")
			}
		})
	}
}

// A symlink pointing out of the layer, followed by a write through it, is the
// same escape wearing a hat.
func TestWritesThroughSymlinksAreRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := image.Unpack(tarOf(t,
		func(w *tar.Writer) {
			_ = w.WriteHeader(&tar.Header{
				Typeflag: tar.TypeSymlink, Name: "link", Linkname: "/tmp", Mode: 0o777,
			})
		},
		file("link/owned", "escaped", 0o644),
	), root)
	if err == nil {
		t.Fatal("a write through a symlink out of the layer was permitted")
	}
}

// Ordinary contents survive intact.
func TestUnpackWritesFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := image.Unpack(tarOf(t,
		file("bin/sh", "#!/bin/sh\n", 0o755),
		file("etc/os-release", "NAME=test\n", 0o644),
	), root)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(root, "bin", "sh"))
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "#!/bin/sh\n" {
		t.Errorf("content is %q", b)
	}

	fi, err := os.Stat(filepath.Join(root, "bin", "sh"))
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode is %v, want 0755", fi.Mode().Perm())
	}
}

// I8 through the image path. A tar carries whole seconds in its ustar header
// and nanoseconds only in a PAX extension; dropping the extension would clamp
// every base image to second precision, which is the exact defect that makes
// cargo rebuild the world.
func TestNanosecondMtimesSurviveUnpacking(t *testing.T) {
	t.Parallel()

	stamp := time.Unix(1700000000, 123456789)

	root := t.TempDir()

	err := image.Unpack(tarOf(t, func(w *tar.Writer) {
		_ = w.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: "f", Mode: 0o644, Size: 1,
			ModTime: stamp, Format: tar.FormatPAX,
		})
		_, _ = w.Write([]byte("x"))
	}), root)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(root, "f"))
	if err != nil {
		t.Fatal(err)
	}

	if got := fi.ModTime().Nanosecond(); got != stamp.Nanosecond() {
		t.Errorf("mtime nanoseconds are %d, want %d", got, stamp.Nanosecond())
	}
}
