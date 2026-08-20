//go:build linux

package overlay

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// tmpfsMagic identifies a tmpfs to statfs. From the kernel's magic.h; there is
// no constant for it in x/sys/unix.
const tmpfsMagic = 0x01021994

// Asking for a scratch tmpfs gets one, and not asking does not.
//
// The option is parsed by a pure function with tests of its own, and that is
// exactly the shape this project keeps finding insufficient: a setting that is
// read correctly and acted on nowhere reads identically to a setting that is
// off. So this asks the kernel what the scratch directory actually is.
func TestAskingForAScratchTmpfsGetsOne(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("mounting a tmpfs needs CAP_SYS_ADMIN; run inside a user namespace")
	}

	isTmpfs := func(t *testing.T, dir string) bool {
		t.Helper()

		var fs unix.Statfs_t
		if err := unix.Statfs(dir, &fs); err != nil {
			t.Fatalf("statfs %s: %v", dir, err)
		}

		return fs.Type == tmpfsMagic
	}

	t.Run("asked for", func(t *testing.T) {
		t.Setenv(EnvScratchTmpfs, "64m")

		scratch := t.TempDir()

		if _, err := NewSplit(t.TempDir(), scratch); err != nil {
			t.Skipf("this machine will not mount a tmpfs: %v", err)
		}

		t.Cleanup(func() { _ = unix.Unmount(scratch, unix.MNT_DETACH) })

		if !isTmpfs(t, scratch) {
			t.Error("the scratch directory is not a tmpfs, so the setting was read" +
				" and acted on nowhere - which looks exactly like not setting it")
		}
	})

	t.Run("not asked for", func(t *testing.T) {
		t.Setenv(EnvScratchTmpfs, "")

		scratch := t.TempDir()

		if _, err := NewSplit(t.TempDir(), scratch); err != nil {
			t.Fatal(err)
		}

		if isTmpfs(t, scratch) {
			t.Error("a scratch nobody asked to be a tmpfs is one; a build's output" +
				" would be held in memory without anyone choosing that")
		}
	})

	// And a typo is refused rather than quietly leaving the scratch on disk.
	t.Run("a typo", func(t *testing.T) {
		t.Setenv(EnvScratchTmpfs, "4G8")

		if _, err := NewSplit(t.TempDir(), t.TempDir()); err == nil {
			t.Error("a misspelt size was accepted, and the operator would see the" +
				" old speed with nothing to explain it")
		}
	})
}
