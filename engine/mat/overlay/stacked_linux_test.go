//go:build linux

package overlay_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
)

// A scratch directory that cannot host a mount is relocated to one that can.
//
// **This is every containerised CI runner.** A container's root is overlayfs,
// overlayfs will not stack on overlayfs, and a guest whose scratch is on the
// step's own root therefore cannot materialise its first base - it fails with
// `invalid argument`, which names nothing about the cause. The situation is
// built here rather than waited for: an overlay is mounted, and a directory
// inside it is exactly what a container hands the guest.
//
// `Mountable` has known the way out since before anything used it, and for most
// of this branch's life nothing did - it was reached from tests only, so the
// escape the engine wrote for itself was the one thing production never took.
// That is what this pins (E634).
func TestAScratchOnOverlayfsIsRelocated(t *testing.T) {
	// Not parallel: mounts.
	base := t.TempDir()

	for _, d := range []string{"l", "u", "w", "m"} {
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

	// What a container gives the guest: a directory on the overlay it is
	// already running on.
	inner := filepath.Join(merged, "scratch")

	err = os.MkdirAll(inner, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = overlay.Available(inner)
	if !errors.Is(err, overlay.ErrUnavailable) {
		t.Fatalf("a directory on an overlay reported itself mountable (%v), so"+
			" this machine does not refuse the stack and the test below proves"+
			" nothing", err)
	}

	at, done, err := overlay.Mountable(inner)
	if err != nil {
		t.Fatalf("nowhere would host a mount: %v", err)
	}

	t.Cleanup(done)

	if at == inner {
		t.Fatal("the scratch was left where it cannot be mounted")
	}

	err = overlay.Available(at)
	if err != nil {
		t.Errorf("relocated to %s, which cannot host a mount either: %v", at, err)
	}
}
