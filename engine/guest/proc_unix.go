//go:build unix

package guest

import (
	"os/exec"
	"syscall"
)

// killGroup ends a process group.
//
// The group rather than the process: a step's command is a shell that starts
// children, and killing the shell alone leaves them holding the step's
// filesystem open.
func killGroup(pgid int, sig syscall.Signal) error {
	return syscall.Kill(pgid, sig) //nolint:wrapcheck // the caller says which process
}

// ownGroup puts a command in a process group of its own, so killGroup can end it
// without reaching anything that started this one.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.Setpgid = true
}
