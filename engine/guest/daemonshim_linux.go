//go:build linux

package guest

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// prepareShim gives the daemon the writable `/run` it will not start without.
//
// A tmpfs rather than a bind: it is thrown away with the namespace, so a daemon
// that dies badly leaves nothing on the machine, and two daemons cannot see each
// other's plugin sockets.
func prepareShim() error {
	err := unix.Mount("none", "/run", "tmpfs", 0, "")
	if err != nil {
		return fmt.Errorf("mount a private /run: %w%s", err, sysAdminHint(err))
	}

	return nil
}

// namespaced puts the shim where it can be root with a `/run` of its own.
func namespaced(a *syscall.SysProcAttr) *syscall.SysProcAttr {
	return namespacedAs(a, os.Getuid(), os.Getgid())
}

// namespacedAs is namespaced with the identity made explicit, so both branches
// can be tested from one process.
//
// **A user namespace only when one is needed.** It exists for a single reason -
// `dockerd` refuses to start unless it is root (E373) - and a guest that is
// already root has that satisfied. Asking anyway nests a namespace, and nesting
// is not free: at one level of user namespace every shape works, and adding a
// *pid* namespace breaks the inner one with `fork/exec: permission denied`,
// because the parent writes `/proc/<pid>/uid_map` through a `/proc` that does
// not match the pid namespace, so the child never receives its mapping and execs
// as nobody (E377). The guest is often already in a user namespace (E105), which
// makes this the common case rather than the exotic one.
//
// The mount namespace is asked for either way: the private `/run` is the other
// half of what the daemon needs, and root-in-a-namespace already carries the
// capability to mount one.
//
// Where a namespace *is* created, root in it maps back to the invoking user
// outside it, so the daemon's files on the guest's disk belong to whoever ran
// the build - which is what makes a named cache readable by the next one (E365).
func namespacedAs(a *syscall.SysProcAttr, uid, gid int) *syscall.SysProcAttr {
	a.Cloneflags |= syscall.CLONE_NEWNS
	a.Unshareflags |= syscall.CLONE_NEWNS

	if uid == 0 {
		return a
	}

	a.Cloneflags |= syscall.CLONE_NEWUSER
	a.UidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: uid, Size: 1}}
	a.GidMappings = []syscall.SysProcIDMap{{ContainerID: 0, HostID: gid, Size: 1}}

	// setgroups must be denied before a gid map can be written by an
	// unprivileged process. The kernel requires it; it is not a choice.
	a.GidMappingsEnableSetgroups = false

	return a
}
