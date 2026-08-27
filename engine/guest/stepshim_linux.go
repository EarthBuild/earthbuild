//go:build linux

package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// prepareStep gives the step a `/proc` that describes the step.
//
// **This is the whole reason the shim exists.** A step runs in a PID namespace
// of its own, so its shell is pid 1 - but `/proc` mounted by the guest before
// the clone describes the guest's namespace, and the step then reads `$$` as 1
// and `/proc/self` as something else entirely. Anything consulting `/proc/$$`
// lands on another process (E705).
//
// A `proc` mount shows the namespace of whoever mounts it, and this process is
// already inside the step's: that is what re-executing between clone and exec
// buys, and it is the only way to get it, since Go cannot run code there.
//
// Mounted at the path outside the root, because the chroot has not happened yet.
func prepareStep(sh *stepShim) error {
	// **In here, because there is nowhere else.** The step has a UTS namespace
	// of its own, and an unset hostname in a new namespace is the machine's -
	// so a step read whatever box it landed on. This process is already inside
	// that namespace, which is the same reason /proc is mounted here rather
	// than by the guest (E758).
	//
	// Reported by not being fatal: a step whose name is the machine's builds
	// correctly and reproduces badly, which is worth continuing for.
	_ = unix.Sethostname([]byte(SandboxHost))

	at := filepath.Join(sh.root, "proc")

	err := os.MkdirAll(at, 0o555)
	if err != nil {
		return fmt.Errorf("make room for /proc: %w", err)
	}

	err = unix.Mount("proc", at, "proc", 0, "")
	if err != nil {
		return fmt.Errorf("mount /proc for the step: %w%s", err, sysAdminHint(err))
	}

	return nil
}

// enterStep puts this process inside the step's filesystem.
//
// Separate from the exec so the failure is attributable: a chroot that fails and
// an exec that fails are different faults, and reporting "exec failed" for a
// root that was not there sends the reader to the wrong place.
func enterStep(sh *stepShim) error {
	err := syscall.Chroot(sh.root)
	if err != nil {
		return fmt.Errorf("enter the step's filesystem at %s: %w", sh.root, err)
	}

	// **After the chroot, and named from inside it.** The working directory the
	// step asked for is a path in its own filesystem; resolving it before would
	// name a directory on the guest.
	dir := stepDir(sh.dir)

	err = syscall.Chdir(dir)
	if err != nil {
		return fmt.Errorf("enter the working directory %s: %w", dir, err)
	}

	return nil
}
