package bulk_test

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/bulk"
)

// An archive cannot write through a symlink it planted itself.
//
// **The name is checked and the link target was not.** `within` refuses an
// absolute name and any `..` segment, which stops the obvious traversal. It
// says nothing about where a symlink *points*, so an archive could ship
// `esc -> ../../..` and then an entry named `esc/file` - a name with no `..`
// in it and not absolute, so it passes - and the write follows the link out of
// the directory the engine gave the build.
//
// This is an export, so the archive is written inside the sandbox: its names
// and its link targets are the part of this path the untrusted side chooses.
// `engine/image` defends the same vector for layers, with
// `TestALayerCannotWriteThroughAPlantedSymlink`; this is the equivalent for
// the export path, which did not.
func TestAnExportCannotWriteThroughAPlantedSymlink(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	tw := tar.NewWriter(&buf)

	for _, h := range []*tar.Header{
		{Name: "esc", Typeflag: tar.TypeSymlink, Linkname: "../outside", Mode: 0o777},
		{Name: "esc/planted", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}

		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte("here!")); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")

	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}

	// The unpack may refuse, which is the preferred outcome. What it may not do
	// is succeed and write outside.
	_ = bulk.UnpackTree(bytes.NewReader(buf.Bytes()), root)

	if _, err := os.Stat(filepath.Join(outside, "planted")); err == nil {
		t.Error("an archive wrote outside the directory it was given, by planting" +
			" a symlink and then writing through it")
	}
}
