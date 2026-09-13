package layer_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A manifest yields the layer's content identity, and it is the same one the
// walk produced.
//
// **The identity a cache key can survive a rebuild on.** `Capture.ID` carries
// mtimes (I8), so a deterministic step built twice gives two ids and every key
// derived from them misses - measured, and written down beside `Entry.Content`.
// `Content` is the same digest without times and is already computed, but only
// ever compared; deriving it from a manifest is what lets a *base* be named
// that way without walking it again.
//
// Asserted rather than assumed, as the manifest round trip already is: the
// manifest is not a second format, it is the bytes the digest is over.
func TestAManifestYieldsTheSameContentIDAsTheWalk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "dir", "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("a.txt", filepath.Join(root, "dir", "link")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	took, err := layer.Take(root)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	got, err := layer.ContentFromManifest(m)
	if err != nil {
		t.Fatal(err)
	}

	if got != took.Content {
		t.Errorf("manifest gave %v, the walk gave %v"+
			"\n  a base named from its manifest must be the same base the walk"+
			"\n  named, or the two disagree about what a cache entry describes",
			got, took.Content)
	}
}

// And it is unmoved by a timestamp, which is the whole point.
func TestTheContentIDFromAManifestIgnoresTimes(t *testing.T) {
	t.Parallel()

	idOf := func(t *testing.T, when time.Time) interface{ String() string } {
		t.Helper()

		root := t.TempDir()

		at := filepath.Join(root, "a.txt")
		if err := os.WriteFile(at, []byte("one\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Chtimes(at, when, when); err != nil {
			t.Fatal(err)
		}

		m, err := layer.Manifest(root)
		if err != nil {
			t.Fatal(err)
		}

		id, err := layer.ContentFromManifest(m)
		if err != nil {
			t.Fatal(err)
		}

		return id
	}

	early := idOf(t, time.Unix(1_000_000, 0))
	late := idOf(t, time.Unix(2_000_000, 0))

	if early.String() != late.String() {
		t.Errorf("the same tree stamped at two times gave %s and %s"+
			"\n  a rebuild stamps the wall clock, which is exactly what this"+
			"\n  identity exists not to notice", early, late)
	}
}
