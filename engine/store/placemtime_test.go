package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Placing a tree twice produces the same layer.
//
// **The identity of a placed tree must not depend on when it was placed.** A
// layer is named by what it contains (green paper §3.3), and the same image
// unpacked on two machines, or on one machine on two days, is the same layer or
// the whole content-addressed tier is a lie: two machines cannot share a base
// they name differently, and a re-placed image conflicts with its own cache
// entry forever.
//
// Found in a real store. `alpine:3.22` had been placed twice, months apart, and
// every build since reported the same key claiming two different results - the
// warning was accurate and the cause was here, not in the step.
func TestPlacingATreeTwiceProducesTheSameLayer(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	// A directory, a file and a symlink: the three kinds a tar carries, with
	// times an image would have rather than times this test is making.
	err := os.MkdirAll(filepath.Join(src, "bin"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(src, "bin", "busybox"), []byte("elf"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink("/bin/busybox", filepath.Join(src, "bin", "arch"))
	if err != nil {
		t.Fatal(err)
	}

	// The image's own times, in the past and deepest-first so a parent is not
	// re-dated by stamping its child.
	when := time.Unix(1_600_000_000, 0)

	stamp(t, filepath.Join(src, "bin", "arch"), when)
	stamp(t, filepath.Join(src, "bin", "busybox"), when)
	stamp(t, filepath.Join(src, "bin"), when)
	stamp(t, src, when)

	first := placeAndTake(t, src)

	// Time passes, as it does between two builds.
	second := placeAndTake(t, src)

	if first != second {
		t.Errorf("placing one tree twice produced two layers:"+
			"\n  first  %s\n  second %s"+
			"\n  a layer is named by what it contains, so a placement that puts"+
			"\n  the clock into the identity means two machines cannot share a"+
			"\n  base and a re-placed image conflicts with its own cache entry",
			first, second)
	}
}

// placeAndTake links a tree into a fresh destination and digests the result.
func placeAndTake(t *testing.T, src string) string {
	t.Helper()

	dst := filepath.Join(t.TempDir(), "placed")

	err := LinkTreeExclusive(src, dst)
	if err != nil {
		t.Fatal(err)
	}

	c, err := layer.Take(dst)
	if err != nil {
		t.Fatal(err)
	}

	return c.ID.String()
}

// stamp sets an entry's own modification time, symlinks included.
//
// `layer.Lchtimes` rather than `os.Chtimes`, which follows a link and stamps
// whatever it points at - here, a `/bin/busybox` that does not exist.
func stamp(t *testing.T, p string, when time.Time) {
	t.Helper()

	err := layer.Lchtimes(p, when)
	if err != nil {
		t.Fatal(err)
	}
}
