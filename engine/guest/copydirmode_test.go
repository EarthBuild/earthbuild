package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// A copied directory keeps every bit of its mode, not the low nine.
//
// copyTree already applies directory modes deepest-first, which is the hard
// half and was right. It recorded them with `fi.Mode().Perm()`, which masks to
// 0777 - so setuid, setgid and the sticky bit were dropped on the way, and
// `chmod 1777 /d` came back as 0777.
//
// Sticky on a directory is the one that shows: it means "only the owner may
// delete what is in here", which is what /tmp is for, so losing it changes what
// the directory permits rather than merely how it prints. Measured against
// earthly, which returns 1777.
func TestACopiedDirectoryKeepsItsWholeMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []os.FileMode{
		os.ModeSticky | 0o777,
		os.ModeSetgid | 0o775,
		0o750,
	} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()

			src := filepath.Join(t.TempDir(), "d")

			//nolint:gosec // the mode under test is set below; this is the
			// ordinary directory the build would have made.
			err := os.Mkdir(src, 0o755)
			if err != nil {
				t.Fatal(err)
			}

			err = os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			err = os.Chmod(src, mode)
			if err != nil {
				t.Fatal(err)
			}

			dst := filepath.Join(t.TempDir(), "out")

			err = copyTree(src, dst, copyOpts{})
			if err != nil {
				t.Fatalf("copying a %v directory failed: %v", mode, err)
			}

			got, err := os.Lstat(dst)
			if err != nil {
				t.Fatal(err)
			}

			want := mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
			if got.Mode()&want != want {
				t.Errorf("a %v directory arrived as %v", mode, got.Mode())
			}
		})
	}
}
