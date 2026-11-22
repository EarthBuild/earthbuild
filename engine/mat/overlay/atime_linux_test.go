//go:build linux

package overlay

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// atimeOf and mtimeOf are the two stamps this file compares.
func atimeOf(t *testing.T, p string) time.Time {
	t.Helper()

	var st unix.Stat_t

	err := unix.Stat(p, &st)
	if err != nil {
		t.Fatal(err)
	}

	return time.Unix(st.Atim.Sec, st.Atim.Nsec)
}

func mtimeOf(t *testing.T, p string) time.Time {
	t.Helper()

	var st unix.Stat_t

	err := unix.Stat(p, &st)
	if err != nil {
		t.Fatal(err)
	}

	return time.Unix(st.Mtim.Sec, st.Mtim.Nsec)
}

// Access times do not survive an overlay, so they cannot be S5's source.
//
// The idea is the cheapest one available and worth eliminating properly: a
// read updates a file's atime, the engine already owns the mount, and walking
// the tree afterwards for files whose atime moved would give 𝑅 with no tracer,
// no privilege and no `unsafe` anywhere. It would also need nothing that is not
// already in the standard library.
//
// It does not work, for two independent reasons, and either alone is fatal:
//
//  1. **overlayfs does not record the read.** Neither the lower inode nor the
//     merged one moves, so there is nothing to walk afterwards. Measured on
//     6.12.90 with the overlay mounted `MS_STRICTATIME` - `ST_RELATIME` reads
//     false, so the flag took, and the stamp still did not move.
//  2. **The engine cannot ask for stricter timestamps anyway.** Remounting the
//     lower `MS_BIND|MS_REMOUNT|MS_STRICTATIME` inside the user namespace a step
//     runs in is `EPERM`: a bind remount there may relax restrictions, never
//     tighten them.
//
// The first is the one this test pins, because it is a property of the kernel
// rather than of this engine. A failure here is **not a regression** - it means
// a kernel started propagating access times through an overlay, and the cheapest
// candidate for S5 has become available again. Reopen the design question; do
// not fix the test (E203).
//
// The control matters as much as the case: a plain read of the same file on the
// same filesystem *does* move the stamp, so a failure to observe one through the
// overlay is the overlay's doing and not a filesystem mounted `noatime`.
func TestAccessTimesDoNotSurviveAnOverlay(t *testing.T) { //nolint:paralleltest // see the note above
	// Not parallel: nstest.In re-executes this binary, and the namespace is the
	// point of the measurement - an overlay mounted outside one is not the
	// arrangement a step runs in.
	if !nstest.In(t) {
		return
	}

	err := canMountOverlay(t)
	if err != nil {
		t.Skipf("no overlay here: %v", err)
	}

	dir := t.TempDir()
	for _, sub := range []string{"lower", "upper", "work", "merged"} {
		mkdirErr := os.MkdirAll(filepath.Join(dir, sub), 0o750)
		if mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
	}

	lower := filepath.Join(dir, "lower")
	merged := filepath.Join(dir, "merged")

	for _, n := range []string{"direct.txt", "through.txt"} {
		writeErr := os.WriteFile(filepath.Join(lower, n), []byte("x"), 0o600)
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	}

	// A beat, so an update is distinguishable from the mtime it started at.
	// Freshly written, atime equals mtime, which is the relatime condition for
	// updating - so this needs no special mount to be observable.
	time.Sleep(20 * time.Millisecond)

	// The control. Without it a `noatime` filesystem would make the case below
	// pass while saying nothing.
	direct := filepath.Join(lower, "direct.txt")

	_, err = os.ReadFile(direct)
	if err != nil {
		t.Fatal(err)
	}

	if !atimeOf(t, direct).After(mtimeOf(t, direct)) {
		t.Skipf("a plain read did not move the access time on %s, so this"+
			" filesystem records none and the case below would prove nothing",
			dir)
	}

	opts := "lowerdir=" + lower +
		",upperdir=" + filepath.Join(dir, "upper") +
		",workdir=" + filepath.Join(dir, "work")

	err = unix.Mount("overlay", merged, "overlay", unix.MS_STRICTATIME, opts)
	if err != nil {
		t.Skipf("overlay would not mount: %v", err)
	}

	t.Cleanup(func() { _ = unix.Unmount(merged, unix.MNT_DETACH) })

	_, err = os.ReadFile(filepath.Join(merged, "through.txt"))
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		filepath.Join(lower, "through.txt"),
		filepath.Join(merged, "through.txt"),
	} {
		if atimeOf(t, p).After(mtimeOf(t, p)) {
			t.Errorf("a read through the overlay moved the access time of %s"+
				"\n  this kernel records what an overlay serves, and access"+
				" times are worth reconsidering as S5's source"+
				"\n  see E203 - reopen the design question rather than"+
				" adjusting this test", p)
		}
	}
}
