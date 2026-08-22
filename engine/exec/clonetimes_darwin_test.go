//go:build darwin

package exec

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A cloned tree carries the times its source carried.
//
// `clonefile(2)` is documented as copying metadata and does - for files and for
// symlinks. **Not for directories**, which come out stamped with now:
//
//	source          clone
//	bin  2020-09-13  bin  2026-08-22   <- the day it was cloned
//	bin/busybox 2020-09-13  bin/busybox 2020-09-13
//
// A layer's identity covers every entry's mtime (§3.3), so a base image placed
// by cloning is named by the day it was placed. Two machines cannot then share
// it, and a re-placed image conflicts with its own cache entry forever - which
// is what a real store was doing, on every build, with the warning naming the
// step rather than the placement (E545).
func TestACloneCarriesTheTimesOfItsSource(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	src := filepath.Join(base, "src")

	err := os.MkdirAll(filepath.Join(src, "bin"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(src, "bin", "busybox"), []byte("elf"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	when := time.Unix(1_600_000_000, 0)

	// Deepest first: stamping a child does not re-date its parent, but creating
	// one does.
	for _, p := range []string{filepath.Join(src, "bin", "busybox"), filepath.Join(src, "bin"), src} {
		err = layer.Lchtimes(p, when)
		if err != nil {
			t.Fatal(err)
		}
	}

	dst := filepath.Join(base, "dst")

	err = cloneTree(src, dst)
	if err != nil {
		t.Skipf("this filesystem cannot clone: %v", err)
	}

	for _, rel := range []string{".", "bin", "bin/busybox"} {
		fi, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil {
			t.Fatal(err)
		}

		if !fi.ModTime().Equal(when) {
			t.Errorf("%s carries %v, and its source carries %v"+
				"\n  a placed tree named by when it was placed is a layer no two"+
				"\n  machines agree about", rel, fi.ModTime().UTC(), when.UTC())
		}
	}
}
