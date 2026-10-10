//go:build linux

package guest

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// CanIsolate reports whether this process can confine a step.
//
// The probe is the operation itself - a mount - rather than a list of
// capabilities to consult and get wrong, and rather than `Getuid() == 0`, which
// would refuse a machine that grants CAP_SYS_ADMIN to an unprivileged user.
//
// Real API rather than a test helper because two packages' tests need the same
// answer and a rule implemented twice drifts, and because the engine has a use
// for it: a step that cannot be confined is refused (A3), and refusing with
// "operation not permitted" tells a reader nothing about which permission.
func CanIsolate() error {
	dir, err := os.MkdirTemp("", "earth-iso-probe-*")
	if err != nil {
		return fmt.Errorf("probe isolation: %w", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	err = unix.Mount("tmpfs", dir, "tmpfs", 0, "")
	if err != nil {
		return fmt.Errorf("this process cannot create a mount for a step: %w", err)
	}

	_ = unix.Unmount(dir, unix.MNT_DETACH)

	// And procfs, which is not the same question. An unprivileged user
	// namespace may mount proc only when it owns the pid namespace being
	// exposed, so a process can mount tmpfs and be refused proc - which is
	// exactly what a namespace made with CLONE_NEWUSER|CLONE_NEWNS and no
	// CLONE_NEWPID gets.
	//
	// A step mounts both. A probe that asked only about the easier one answered
	// "this machine can isolate" and left the step to fail with an unexplained
	// EPERM half a second later: a probe with fewer outcomes than the world,
	// which is what the store's case-sensitivity check was (E97, E122).
	err = unix.Mount("proc", dir, "proc", 0, "")
	if err != nil {
		return fmt.Errorf("this process cannot mount /proc for a step"+
			" (an unprivileged user namespace can only mount procfs for a pid"+
			" namespace it owns): %w", err)
	}

	_ = unix.Unmount(dir, unix.MNT_DETACH)

	return nil
}
