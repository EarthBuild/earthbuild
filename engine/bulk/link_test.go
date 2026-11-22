package bulk

import (
	"archive/tar"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A link lands inside the export's directory even where nothing checked its
// parent.
//
// `within` refusing a parent that resolves out of the root is the guard. This
// is the floor under it: the link is placed through an `os.Root`, whose walk
// will not leave the root, so `unpackEntry` handed an unchecked path still
// cannot plant one outside. The link's *target* is the build's business.
func TestALinkIsPlacedInsideTheRootEvenUnchecked(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")

	for _, d := range []string{root, outside} {
		err := os.MkdirAll(d, 0o750)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := os.Symlink(outside, filepath.Join(root, "esc"))
	if err != nil {
		t.Fatal(err)
	}

	fs, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = fs.Close() }()

	hdr := &tar.Header{Name: "esc/planted", Typeflag: tar.TypeSymlink, Linkname: "/etc"}

	err = unpackEntry(nil, hdr, fs, filepath.Join(root, "esc", "planted"))
	if err == nil {
		t.Error("a link was placed through esc -> " + outside +
			"\n  its parent resolves out of the root, so it landed outside it")
	}

	_, err = os.Lstat(filepath.Join(outside, "planted"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s/planted exists (%v): the link was written outside the root", outside, err)
	}
}
