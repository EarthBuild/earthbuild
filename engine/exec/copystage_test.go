package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// copyDirExcluding is what stages a build context on the host, and it is the
// per-file path a large context pays over and over. Anything done to make it
// cheaper has to keep these, which is why they are written down before it is
// touched: modes, symlinks, nesting, and empty directories.
func TestAStagedTreeKeepsItsShape(t *testing.T) {
	t.Parallel()

	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "out")

	mk := func(p string, mode os.FileMode) {
		t.Helper()

		full := filepath.Join(src, p)
		err := os.MkdirAll(filepath.Dir(full), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(full, []byte("body of "+p), mode)
		if err != nil {
			t.Fatal(err)
		}
	}

	mk("top.txt", 0o600)
	mk("a/b/c/deep.sh", 0o750)
	mk("a/readonly.txt", 0o400)

	err := os.MkdirAll(filepath.Join(src, "a/empty"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("../top.txt", filepath.Join(src, "a/link"))
	if err != nil {
		t.Fatal(err)
	}

	err = copyDirExcluding(src, dst, nil)
	if err != nil {
		t.Fatalf("stage the tree: %v", err)
	}

	for _, c := range []struct {
		path string
		mode os.FileMode
	}{
		{"top.txt", 0o600},
		{"a/b/c/deep.sh", 0o750},
		{"a/readonly.txt", 0o400},
	} {
		fi, statErr := os.Lstat(filepath.Join(dst, c.path))
		if statErr != nil {
			t.Errorf("%s: %v", c.path, statErr)

			continue
		}

		if fi.Mode().Perm() != c.mode {
			t.Errorf("%s: mode %v, want %v", c.path, fi.Mode().Perm(), c.mode)
		}

		b, readErr := os.ReadFile(filepath.Join(dst, c.path))
		if readErr != nil || string(b) != "body of "+c.path {
			t.Errorf("%s: contents %q (%v)", c.path, b, readErr)
		}
	}

	// A symlink is copied as a symlink, not as what it points at.
	got, linkErr := os.Readlink(filepath.Join(dst, "a/link"))
	if linkErr != nil || got != "../top.txt" {
		t.Errorf("a/link -> %q (%v), want ../top.txt", got, linkErr)
	}

	// An empty directory is still a directory: nothing walks into it, so it is
	// the one a copy driven by files alone would lose.
	fi, dirErr := os.Stat(filepath.Join(dst, "a/empty"))
	if dirErr != nil || !fi.IsDir() {
		t.Errorf("a/empty: %v (dir=%v)", dirErr, fi != nil && fi.IsDir())
	}

	if fi != nil && fi.Mode().Perm() != 0o700 {
		t.Errorf("a/empty: mode %v, want 0700", fi.Mode().Perm())
	}
}
