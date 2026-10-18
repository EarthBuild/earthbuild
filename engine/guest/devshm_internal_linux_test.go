package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// Every step gets a /dev/shm.
//
// POSIX shared memory is a file in a tmpfs at this path and nowhere else, so a
// step without one has no `sem_open`, no `shm_open` and no `multiprocessing`.
// The OCI runtime specification mounts it, which means every other engine a
// build has ever run under provided it, and the failures when it is absent name
// the thing that used it rather than the mount: Python's multiprocessing says a
// semaphore does not exist, Chrome dies on its first tab, and PostgreSQL will
// not start (E752).
func TestEveryStepGetsSharedMemory(t *testing.T) {
	t.Parallel()

	var (
		devAt = -1
		shm   *Mount
	)

	for i, m := range deviceMounts() {
		switch m.Target {
		case "/dev":
			devAt = i
		case "/dev/shm":
			shm = &deviceMounts()[i]

			// After /dev, which is mounted over: a /dev arriving later would
			// hide what was mounted inside it.
			if devAt == -1 {
				t.Error("/dev/shm is mounted before the /dev it lands in")
			}
		}
	}

	if shm == nil {
		t.Fatal("no /dev/shm among the mounts every step gets")
	}

	if !shm.Tmpfs {
		t.Error("/dev/shm is not a tmpfs, so what a step puts there reaches a disk")
	}

	if !shm.Ephemeral {
		t.Error("/dev/shm is not ephemeral, so what a step puts there is captured")
	}

	// 1777 as everywhere else: world-writable, and sticky so one user cannot
	// remove another's segment.
	if shm.Mode != 0o1777 {
		t.Errorf("/dev/shm mode = %#o, want %#o", shm.Mode, 0o1777)
	}
}

// The sticky bit asked for is the sticky bit set.
//
// `os.FileMode.Perm()` masks to the low nine bits, so a mount asking for 1777
// was chmodded to 0777 and the sticky bit was dropped in silence - which is
// visible only as one user being able to delete another's shared memory, long
// after the build (E752).
func TestAMountModeKeepsItsStickyBit(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "shm")

	err := os.Mkdir(dir, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = applyMode(dir, Mount{Target: "/dev/shm", Mode: 0o1777}, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode()&os.ModeSticky == 0 {
		t.Errorf("mode is %v, and the sticky bit asked for is not in it", fi.Mode())
	}

	if fi.Mode().Perm() != 0o777 {
		t.Errorf("permissions are %#o, want %#o", fi.Mode().Perm(), 0o777)
	}
}
