//go:build linux

package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A container whose root is overlayfs is the ordinary case for a build tool,
// and there the kernel rejects the mount with a bare "invalid argument". The
// error must name the cause and the way out; a diagnostic nobody can act on is
// the same as no diagnostic.
func TestOverlayOnOverlayExplainsItself(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var st unix.Statfs_t
	err := unix.Statfs(dir, &st)
	if err != nil {
		t.Skip(err)
	}

	if int64(st.Type) != overlayfsMagic { //nolint:unconvert // this field is not this width on every platform
		t.Skip("this filesystem is not overlayfs, so the stacking case cannot arise here")
	}

	// `Geteuid() == 0` used to stand here, and it is the wrong question - the
	// same one `CanIsolate`'s doc comment rejects by name. In a build container
	// euid is 0 and mounting is refused anyway, so this ran and failed for
	// wanting a message the kernel never got far enough to produce.
	err = canMountOverlay(t)
	if err != nil {
		t.Skipf("this environment refuses an overlay mount outright, so the"+
			" stacking case cannot be reached: %v", err)
	}

	m, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	stack := make([]ir.NodeID, 0, 2)

	for i := range 2 {
		id := ir.NodeID{byte(i + 1)}
		writeErr := m.WriteLayer(id, map[string]string{"f": "x"})
		if writeErr != nil {
			t.Fatal(writeErr)
		}

		stack = append(stack, id)
	}

	_, err = m.Materialise(t.Context(), stack)
	if err == nil {
		t.Skip("overlay stacked successfully; this kernel permits it")
	} else {
		for _, want := range []string{"overlayfs cannot stack on overlayfs", "mount a volume"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not mention %q:\n%s", want, err)
			}
		}
	}
}

// canMountOverlay reports whether an overlay can be mounted here at all.
//
// The probe is the operation itself, which is the rule `guest.CanIsolate`
// states and the one this file was not following. A test about *which* error a
// stacked overlay produces cannot say anything on a machine that refuses the
// mount before it looks at the layers - and "operation not permitted" is not
// evidence about stacking, it is evidence about permission.
func canMountOverlay(t *testing.T) error {
	t.Helper()

	dir := t.TempDir()

	for _, sub := range []string{"lower", "upper", "work", "merged"} {
		err := os.MkdirAll(filepath.Join(dir, sub), 0o750)
		if err != nil {
			return err
		}
	}

	opts := "lowerdir=" + filepath.Join(dir, "lower") +
		",upperdir=" + filepath.Join(dir, "upper") +
		",workdir=" + filepath.Join(dir, "work")

	err := unix.Mount("overlay", filepath.Join(dir, "merged"), "overlay", 0, opts)
	if err != nil {
		return err
	}

	_ = unix.Unmount(filepath.Join(dir, "merged"), unix.MNT_DETACH)

	return nil
}
