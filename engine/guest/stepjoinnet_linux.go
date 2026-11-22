//go:build linux

package guest

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// joinStepNet puts this process in the network namespace the guest named, if it
// named one.
//
// **In the shim, for the reason everything else here is.** Go cannot run code
// between clone and exec, so a step's namespaces are entered by re-executing
// this binary and doing the work in the child. `setns` is no different: it has
// to happen in the process that becomes the step, and there is no other moment.
//
// Joining, not unsharing. `CLONE_NEWNET` on `SysProcAttr` would give the step an
// empty namespace with no route out, which is the option `isolate` weighs and
// rejects - a build that cannot fetch a dependency is no use. The guest has
// already built a namespace with a veth and a way out; this walks into it.
//
// Before the chroot, because /var/run/netns is the guest's path and the step's
// filesystem does not contain it.
func joinStepNet() error {
	at := os.Getenv(EnvStepNetNS)
	if at == "" {
		return nil
	}

	// O_CLOEXEC, so the descriptor does not survive into the step. A step
	// holding an open handle on its own network namespace could pass it on, and
	// the step is the part of this that runs somebody else's code.
	fd, err := unix.Open(at, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open the step's network namespace at %s: %w"+
			"\n  the guest makes this before the step starts, so its absence is"+
			" the guest's fault and not the Earthfile's", at, err)
	}

	defer func() { _ = unix.Close(fd) }()

	err = unix.Setns(fd, unix.CLONE_NEWNET)
	if err != nil {
		return fmt.Errorf("join the step's network namespace at %s: %w%s",
			at, err, sysAdminHint(err))
	}

	return nil
}
