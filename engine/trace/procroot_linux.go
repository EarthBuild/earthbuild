//go:build linux

package trace

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// procRoot is the procfs whose pids are the ones a notification carries.
//
// `/proc` by default, and that is wrong wherever the engine is pid 1 of a pid
// namespace with the *host's* procfs still mounted. The two disagree silently:
// a notification's pid is in the reader's pid namespace, `/proc/<pid>` resolves
// against whatever procfs is mounted, and if that procfs came from another
// namespace the number names a different process entirely.
//
// Measured, and it is not a corner: `getpid()` returned 1 while
// `/proc/self/status` said `Pid: 2031341`, so every path read was attempted
// against a host process of the same number - EACCES on the ones that exist and
// ENOENT on the ones that do not, and **no RUN was ever observed** (E216).
var procRoot = "/proc"

// UseProcAt points pid lookups at a procfs mounted here.
//
// For a caller that has had to mount its own, which is what a process finding
// itself pid 1 with a foreign `/proc` must do. Idempotent and not concurrent
// with tracing: call it before any tracer is started.
func UseProcAt(dir string) {
	procRoot = dir
}

// ProcIsOurs reports whether a procfs shows this process's own pid namespace.
//
// The check is exact rather than heuristic: `Pid:` in a status file is rendered
// by the procfs being read, in *its* pid namespace, while `getpid` answers in
// the caller's. Equal means the two agree; different means every pid taken from
// one and used against the other names the wrong process.
func ProcIsOurs(dir string) (bool, error) {
	b, err := os.ReadFile(dir + "/self/status") //nolint:gosec // a procfs path
	if err != nil {
		return false, fmt.Errorf("read %s/self/status: %w", dir, err)
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "Pid:")
		if !ok {
			continue
		}

		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return false, fmt.Errorf("parse %q: %w", line, err)
		}

		return n == os.Getpid(), nil
	}

	return false, fmt.Errorf("%s/self/status has no Pid line", dir)
}

// MountPrivateProc mounts a procfs for this process's own pid namespace.
//
// Only for a caller that has found `/proc` is somebody else's. Mounting one
// needs CAP_SYS_ADMIN in the user namespace and a mount namespace of one's own,
// which is what the guest already has - and it is mounted somewhere private
// rather than over `/proc`, because replacing `/proc` changes what every other
// part of the engine and every step sees.
func MountPrivateProc(dir string) error {
	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		return fmt.Errorf("make %s: %w", dir, err)
	}

	err = unix.Mount("proc", dir, "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "")
	if err != nil {
		return fmt.Errorf("mount a procfs at %s: %w"+
			"\n  this needs CAP_SYS_ADMIN in this user namespace and a mount"+
			" namespace of this process's own", dir, err)
	}

	return nil
}

// UnmountProc takes away a procfs MountPrivateProc put somewhere.
//
// For the caller that mounted one and then found it is not this namespace's
// after all: the directory underneath cannot be removed while a mount is on it,
// so leaving it mounted leaves the directory too (E473).
//
// `MNT_DETACH` rather than a plain unmount: nothing of ours is reading it, and a
// busy mount would otherwise refuse and leave exactly the litter this exists to
// prevent.
func UnmountProc(dir string) error {
	err := unix.Unmount(dir, unix.MNT_DETACH)
	if err != nil {
		return fmt.Errorf("unmount the procfs at %s: %w", dir, err)
	}

	return nil
}
