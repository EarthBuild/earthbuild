//go:build !linux

package guest

import (
	"errors"
	"os/exec"
)

// ErrCannotIsolate reports that this platform cannot confine a step.
//
// Off Linux there are no namespaces to use, so the guest cannot satisfy green
// paper A3 and must refuse rather than run a step whose result would look
// cacheable without being so.
//
// In production this never fires: the guest runs inside a Linux VM. It fires on
// a developer's Mac running the guest natively, which is exactly where a silent
// pretence would be most tempting.
var ErrCannotIsolate = errors.New("cannot isolate the step: requires linux")

func isolate(*exec.Cmd, string, bool) error { return ErrCannotIsolate }

// isolateShim is isolate, and refused for the same reason.
func isolateShim(*exec.Cmd, string, bool) error { return ErrCannotIsolate }

func isolationAvailable() bool { return false }
