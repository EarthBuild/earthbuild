package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// A directory artifact keeps the mode the build gave it.
//
// Modes are part of an artifact (I8), and a directory's mode is the half that
// nothing was checking: the export tests compare files, so `SAVE ARTIFACT /d AS
// LOCAL out/d` was free to hand back a directory with a different mode than the
// one the build made. Measured against earthly, which returns 1777 where this
// returned 755.
//
// Two things went wrong on the way and this covers the second. `os.MkdirAll`
// applies the process umask, so a mode is a request rather than an instruction:
// 0777 asked for under the usual 022 arrives as 0755. Only an explicit chmod
// sets a mode.
//
// The unwritable case is the sharper one. A directory the build left at 0500
// cannot be written into after it is created, so the walk that creates it
// top-down and then copies its contents in fails outright - which is why the
// guest applies directory modes deepest-first once everything is in place, and
// why the host has to do the same rather than trust MkdirAll.
func TestADirectoryArtifactKeepsItsMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mode os.FileMode
	}{
		{"sticky", os.ModeSticky | 0o777},
		{"unwritable", 0o500},
		{"group-writable", 0o775},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := filepath.Join(t.TempDir(), "d")

			//nolint:gosec // the mode under test is set below; this is the
			// ordinary directory the build would have made.
			err := os.Mkdir(src, 0o755)
			if err != nil {
				t.Fatal(err)
			}

			err = os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			// Set after the contents, for the reason the copy has to: the
			// directory cannot be written into once it is 0500.
			err = os.Chmod(src, tc.mode)
			if err != nil {
				t.Fatal(err)
			}

			dst := filepath.Join(t.TempDir(), "out")

			// Both trees have to be writable again before the temporary
			// directories are removed, or an unwritable directory that the copy
			// handled correctly fails the test in cleanup instead.
			t.Cleanup(func() {
				//nolint:gosec // directories, and only so cleanup can remove them
				_ = os.Chmod(src, 0o700)
				//nolint:gosec // as above
				_ = os.Chmod(dst, 0o700)
			})

			err = copyDir(src, dst)
			if err != nil {
				t.Fatalf("copying a %v directory failed: %v", tc.mode, err)
			}

			got, err := os.Lstat(dst)
			if err != nil {
				t.Fatal(err)
			}

			if got.Mode().Perm() != tc.mode.Perm() ||
				got.Mode()&os.ModeSticky != tc.mode&os.ModeSticky {
				t.Errorf("a %v directory arrived as %v", tc.mode, got.Mode())
			}

			// The contents still have to be there: a mode applied too early
			// makes the copy fail, and one applied to the wrong thing loses it.
			_, err = os.Lstat(filepath.Join(dst, "f.txt"))
			if err != nil {
				t.Errorf("the directory's contents did not arrive: %v", err)
			}
		})
	}
}
