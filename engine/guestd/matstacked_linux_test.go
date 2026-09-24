//go:build linux

package guestd

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
)

// The guest gets a materialiser even when its scratch is on an overlay.
//
// **The daemon's wiring, not the helper's.** `overlay.Mountable` knew how to
// escape a scratch that cannot host a mount, and was reached only from tests -
// so on every containerised runner the guest asked for a mount on the step's own
// overlay root, got `invalid argument`, and failed the build. The helper being
// right is no use if nothing calls it, which is what this asserts and what
// TestAScratchOnOverlayfsIsRelocated does not (E634).
func TestAGuestScratchOnOverlayfsStillMaterialises(t *testing.T) { //nolint:paralleltest // see the note above
	// Not parallel: mounts.
	base := t.TempDir()

	for _, d := range []string{"l", "u", "w", "m", "layers"} {
		err := os.MkdirAll(filepath.Join(base, d), 0o750)
		if err != nil {
			t.Fatal(err)
		}
	}

	merged := filepath.Join(base, "m")
	opts := "lowerdir=" + filepath.Join(base, "l") +
		",upperdir=" + filepath.Join(base, "u") +
		",workdir=" + filepath.Join(base, "w")

	err := unix.Mount("overlay", merged, "overlay", 0, opts)
	if err != nil {
		t.Skipf("cannot mount an overlay here, so there is no stack to be refused: %v", err)
	}

	t.Cleanup(func() { _ = unix.Unmount(merged, 0) })

	scratch := filepath.Join(merged, "scratch")

	err = os.MkdirAll(scratch, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	mat, release, err := newMaterialiser(filepath.Join(base, "layers"), scratch)
	if err != nil {
		t.Fatalf("the guest refused a scratch it could have relocated: %v", err)
	}

	t.Cleanup(release)

	// **Carried all the way to a mount, because nothing before it fails.**
	// `NewSplit` makes directories and asks the kernel nothing, so a guest
	// pointed at an unmountable scratch is built quite happily and falls over
	// at the first base it has to assemble. A test that stopped at the
	// constructor passed against a deliberately un-wired engine, which is how
	// this one came to go this far.
	om, ok := mat.(*overlay.Materialiser)
	if !ok {
		t.Fatalf("the guest's materialiser is %T, which this test cannot load", mat)
	}

	id := ir.NodeID{1}

	err = om.WriteLayer(id, map[string]string{"f": "x"})
	if err != nil {
		t.Fatal(err)
	}

	h, err := om.Materialise(t.Context(), []ir.NodeID{id})
	if err != nil {
		t.Fatalf("a step's base would not assemble with the scratch on an"+
			" overlay, which is the whole of E634: %v", err)
	}

	t.Cleanup(func() { _ = h.Release() })
}
