package guest

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// ErrCannotIsolate reports that the guest cannot confine a step.
//
// It is returned rather than swallowed, and that is the whole point. Green
// paper A3 assumes the executor confines a step's writes to its own upper
// layer; a step that escapes invalidates *every* cache claim in the
// specification, because ε no longer bounds what it observed.
//
// So an engine that cannot isolate refuses to run the step (I10). Running it
// anyway would produce a result that looks cacheable and is not, which is worse
// than not running it at all.
var ErrCannotIsolate = errors.New("cannot isolate the step")

// isolate applies confinement to a command that will run against root.
//
// What it does, and what each is for:
//
//   - Chroot(root): the step cannot name a path outside its own filesystem.
//     This is what makes ε bounded and A3 true.
//   - CLONE_NEWNS: a private mount namespace, so mounts the step makes do not
//     propagate back to the guest.
//   - CLONE_NEWPID: the step cannot see or signal the guest's processes, and
//     its children die with it rather than leaking.
//   - CLONE_NEWUTS, CLONE_NEWIPC: hostname and IPC are its own, so two
//     concurrent steps cannot observe each other through them.
//
// Deliberately NOT applied: CLONE_NEWNET. Cutting the network would break every
// build that fetches a dependency, which is most of them. Network isolation is
// a policy decision with a large blast radius, so it is opt-in rather than a
// side effect of turning isolation on.
//
// Resource bounds are applied separately, by newCgroup, and on a different rule:
// isolation is refused when unavailable because it protects ε, whereas an
// unbounded step is still a *correct* step, so cgroups degrade and report why.
func isolate(cmd *exec.Cmd, root string, dropNet bool) error {
	return isolateWith(cmd, root, dropNet, false)
}

// isolateShim is isolate for a step a shim will chroot into, so the chroot is
// left to the shim. Everything else is the same confinement.
func isolateShim(cmd *exec.Cmd, root string, dropNet bool) error {
	return isolateWith(cmd, root, dropNet, true)
}

func isolateWith(cmd *exec.Cmd, root string, dropNet, shimming bool) error {
	if os.Geteuid() != 0 {
		return ErrCannotIsolate
	}

	flags := uintptr(syscall.CLONE_NEWNS |
		syscall.CLONE_NEWPID |
		syscall.CLONE_NEWUTS |
		syscall.CLONE_NEWIPC)

	if dropNet {
		flags |= syscall.CLONE_NEWNET
	}

	// Filled in rather than replaced.
	//
	// It read `cmd.SysProcAttr = &syscall.SysProcAttr{…}`, which discards
	// whatever a caller had already set - and `AttachTerminal` sets `Setsid` and
	// `Setctty` before this runs, so an interactive step got its terminal on the
	// streams and not as a *controlling* terminal. The symptom was a step that
	// spoke on the right terminal and answered `NO-CTTY` (E193).
	//
	// Assignment is the natural way to write this and is wrong for any field
	// somebody adds later, which is the argument for populating.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	// **Not when a shim will do it.** `SysProcAttr.Chroot` is what forces the
	// exec'd file to be inside the root, and a shim that chroots itself does
	// not need it - which is what lets the shim be the guest's own binary at
	// the guest's own path rather than something bound into the step (E705).
	if !shimming {
		cmd.SysProcAttr.Chroot = root
	}

	cmd.SysProcAttr.Cloneflags = flags
	// Unshare the mount namespace so that anything the step mounts is invisible
	// to the guest, and is torn down when it exits.
	cmd.SysProcAttr.Unshareflags = syscall.CLONE_NEWNS

	// Inside the chroot the working directory is the new root, not the path the
	// guest knows it by. With a shim there is no chroot yet when the child
	// starts, so this is a path on the guest and the shim does the chdir once
	// it is inside.
	cmd.Dir = "/"

	return nil
}
