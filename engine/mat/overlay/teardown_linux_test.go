//go:build linux

package overlay

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// Which syscall the teardown is, measured without a shell in the way.
//
// E404 found release costs 9 ms against a mount's 0.6, and checked it with a
// shell loop of `mount` and `umount` - which forks five processes an iteration
// and therefore measured `fork` and `exec` as much as anything. It agreed with
// the Go number by luck: 15 ms against 9 ms is not agreement, and reading it as
// confirmation was the mistake.
//
// *Failure class: a harness that costs more than the thing it measures.* The
// tell is available before the run - count the `exec`s - and this project has
// recorded the same shape as "a null result reported against an uncharacterised
// instrument".
//
// So: in-process, one syscall per timed operation, nothing forked.
func BenchmarkTeardownParts(b *testing.B) {
	if os.Geteuid() != 0 {
		b.Skip("overlayfs needs CAP_SYS_ADMIN; run this inside a user namespace")
	}

	setUp := func(b *testing.B) string {
		b.Helper()

		base := b.TempDir()

		for _, d := range []string{"l", "u", "w", "m"} {
			err := os.MkdirAll(filepath.Join(base, d), 0o750)
			if err != nil {
				b.Fatal(err)
			}
		}

		opts := "lowerdir=" + filepath.Join(base, "l") +
			",upperdir=" + filepath.Join(base, "u") +
			",workdir=" + filepath.Join(base, "w")

		err := unix.Mount("overlay", filepath.Join(base, "m"), "overlay", 0, opts)
		if err != nil {
			b.Skipf("this machine cannot mount overlayfs: %v", err)
		}

		return base
	}

	b.Run("unmount", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()

			base := setUp(b)

			b.StartTimer()

			err := unix.Unmount(filepath.Join(base, "m"), 0)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("unmount-detach", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()

			base := setUp(b)

			b.StartTimer()

			err := unix.Unmount(filepath.Join(base, "m"), unix.MNT_DETACH)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("removeall", func(b *testing.B) {
		for b.Loop() {
			b.StopTimer()

			base := setUp(b)

			err := unix.Unmount(filepath.Join(base, "m"), 0)
			if err != nil {
				b.Fatal(err)
			}

			b.StartTimer()

			err = os.RemoveAll(base)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
