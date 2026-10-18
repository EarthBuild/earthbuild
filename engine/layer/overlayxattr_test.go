package layer_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/layer"
	"golang.org/x/sys/unix"
)

// A layer's identity does not depend on the stack it was assembled over.
//
// overlayfs keeps its own bookkeeping in extended attributes. `user.overlay.
// origin` records the identity of the file in the *lower* layer that an upper
// entry was copied up from, and it is written onto the upper layer - which this
// engine then commits, stores, and hashes.
//
// So a directory a step copied into carries a fingerprint of the layers
// underneath it, and:
//
//	the same copy over two base images produces two layer digests
//	an observation of that directory goes stale whenever the base moves
//
// Measured on a six-COPY project across a bump from alpine:3.21 to 3.22: **one
// of six copies was reused**, and the engine's own diagnosis was `/app changed
// in the base` for the other five (E132). The stored layers carry
// `user.overlay.origin=""` on `/app`.
//
// **This is E121's concern arriving through the door E121 did not test.** That
// experiment asserted the observer and the view compute the same digest, and
// its fixture built layers with `WriteLayer` - which never goes through an
// overlay, so no bookkeeping was ever there to disagree about.
//
// The rule: an attribute describing *how a filesystem was assembled* is not
// part of what is at a path. Green paper §3.3 lists what a layer records, and
// the answer to "which lower inode did this come from" is not on the list.
func TestALayerIgnoresOverlayBookkeeping(t *testing.T) {
	t.Parallel()

	plain := t.TempDir()
	marked := t.TempDir()

	for _, dir := range []string{plain, marked} {
		inner := filepath.Join(dir, "app")

		err := os.MkdirAll(inner, 0o755) //nolint:gosec // matches what a copy creates
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(inner, "f.txt"), []byte("payload\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		// Stamped identically, because `Capture.ID` includes mtimes and two
		// temporary trees are created moments apart - which would make this
		// pass or fail on the clock rather than on the attribute. The first
		// version of this test did exactly that and blamed the code.
		at := time.Unix(1_600_000_000, 0)

		for _, p := range []string{filepath.Join(inner, "f.txt"), inner, dir} {
			err = os.Chtimes(p, at, at)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	// Exactly what a committed layer carries after a copy into a directory
	// that overlayfs had to copy up.
	err := unix.Lsetxattr(filepath.Join(marked, "app"), "user.overlay.origin", []byte{}, 0)
	if err != nil {
		t.Skipf("this filesystem does not take that attribute: %v", err)
	}

	// Setting an attribute touches ctime, and on some filesystems mtime with
	// it, so the stamp is reapplied after.
	at := time.Unix(1_600_000_000, 0)

	err = os.Chtimes(filepath.Join(marked, "app"), at, at)
	if err != nil {
		t.Fatal(err)
	}

	a, err := layer.Take(plain)
	if err != nil {
		t.Fatal(err)
	}

	b, err := layer.Take(marked)
	if err != nil {
		t.Fatal(err)
	}

	if a.ID != b.ID {
		t.Errorf("overlayfs bookkeeping changed a layer's identity:"+
			"\n  without user.overlay.origin  %s"+
			"\n  with it                      %s"+
			"\n  the same step over two bases then produces two layers, and every"+
			"\n  prediction about the directory goes stale when the base moves", a.ID, b.ID)
	}
}

// A user's own extended attribute still counts.
//
// The companion, because "ignore overlay attributes" is satisfiable by ignoring
// all of them - and then `setcap` on a binary stops reaching the image, which
// is the defect E92 fixed by carrying them in the first place.
func TestALayerStillRecordsRealXattrs(t *testing.T) {
	t.Parallel()

	plain := t.TempDir()
	marked := t.TempDir()

	for _, dir := range []string{plain, marked} {
		err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("payload\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := unix.Lsetxattr(filepath.Join(marked, "f.txt"), "user.earthbuild.real", []byte("v"), 0)
	if err != nil {
		t.Skipf("this filesystem does not take extended attributes: %v", err)
	}

	a, err := layer.Take(plain)
	if err != nil {
		t.Fatal(err)
	}

	b, err := layer.Take(marked)
	if err != nil {
		t.Fatal(err)
	}

	if a.ID == b.ID {
		t.Error("an extended attribute a step set no longer reaches the layer's" +
			" identity, so a `setcap` grant would be lost again (E92)")
	}
}
