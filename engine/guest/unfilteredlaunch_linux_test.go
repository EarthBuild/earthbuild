//go:build linux

package guest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seccompOfCallingThread reports the `Seccomp:` field for the thread the caller
// is running on, or "" when procfs cannot answer.
//
// `/proc/thread-self` rather than `/proc/self`: the question is about one
// thread, and the process-wide file answers for the group leader.
func seccompOfCallingThread() string {
	b, err := os.ReadFile("/proc/thread-self/status")
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "Seccomp:")
		if ok {
			return strings.TrimSpace(rest)
		}
	}

	return ""
}

// The thread a step is started from must not already carry the filter.
//
// **This is the E723 deadlock, stated as an invariant.** Go's `os/exec` clones
// with `CLONE_VM | CLONE_VFORK` unless `CLONE_NEWUSER` is asked for, so the
// thread that starts a step is suspended in `kernel_clone` until the child
// execs. If that thread is filtered, the child inherits the filter and its very
// first syscall - the `execve` - traps to a user notification. The supervisor
// that would answer it is a goroutine in the same process, and the runtime
// cannot get past the thread stopped in vfork. Nobody answers, the child never
// execs, the parent never returns (E723, E729).
//
// Measured at about two cold builds in five on a 45-step Earthfile, and zero in
// fifteen with the filter never installed - so the filter's presence at clone
// time is necessary for the hang, and its absence is the fix.
//
// The step shim is what makes the absence possible: the shim is exec'd with no
// filter anywhere, which releases the vfork at once, and installs the filter on
// itself afterwards - on a thread that goes on to *become* the step. That is
// what `trace.InstallOnSelf` is written for (E730).
//
//nolint:paralleltest // runs a step, which the traced path gives a thread of its own
func TestTheThreadAStepIsStartedFromIsNotFiltered(t *testing.T) {
	root := t.TempDir()

	present := filepath.Join(root, "read-by-the-step")

	err := os.WriteFile(present, []byte("contents"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Read inside the closure, because the closure is what clones. Asking
	// anywhere else would answer for a thread that is not the one at risk.
	var launcher string

	step := func(channel *os.File) ([]byte, error) {
		launcher = seccompOfCallingThread()

		// What a shim that cannot install a filter says, so this test spends no
		// time in the listener deadline: a message with no descriptor in it.
		if channel != nil {
			_, _ = channel.Write([]byte{0})
		}

		return exec.Command("/bin/sh", "-c", "cat "+present).CombinedOutput()
	}

	// The shim arrangement, which is what a confined step uses. The closure
	// stands in for the launch: what is being asserted is the state of the
	// thread it is called on, not what it goes on to start.
	_, _, err = runObservedViaShim(step, func(string) error { return nil }, func() {})
	if err != nil {
		t.Fatalf("the step failed: %v", err)
	}

	if launcher == "" {
		t.Skip("procfs does not report Seccomp here, so the invariant cannot be checked")
	}

	if launcher != "0" {
		t.Fatalf("a step was started from a thread whose Seccomp field is %q, want \"0\""+
			"\n  os/exec clones with CLONE_VM|CLONE_VFORK, so this thread is"+
			" suspended until the child execs - and a filtered thread means the"+
			" child's execve traps to a supervisor that the vfork is preventing"+
			" from running"+
			"\n  install the filter in the step shim instead, after its own exec"+
			" has released the vfork (E723, E729, E730)", launcher)
	}
}
