//go:build linux

package trace

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestPinConfinesAThreadAndWhatItForks.
//
// **The tracer is cheap; waking it on another vCPU is not.** A traced path call
// costs 2.2µs when the stopped thread and the thread answering it share a CPU
// and 45µs when they do not - measured in the same 4-vCPU guest, so the
// difference is the round trip and nothing else (E681). Under a hypervisor both
// halves of that wakeup are vmexits, which is why bare metal barely notices
// (8.3µs against 7.2µs) and the VM notices 19x.
//
// Two properties, and the second is the one the arrangement leans on: a step is
// started from the same locked thread that carries the seccomp filter, so it
// inherits that thread's affinity across fork the way it inherits the filter.
// Nothing else pins the step, and if that inheritance stopped holding the step
// would roam while the tracer stayed put - the worst of both.
func TestPinConfinesAThreadAndWhatItForks(t *testing.T) {
	if runtime.NumCPU() < 2 {
		t.Skip("a machine with one CPU cannot show a thread confined to one of them")
	}

	type outcome struct {
		mask  unix.CPUSet
		child string
		err   error
	}

	// A goroutine that locks and never unlocks: an affinity left on a thread
	// returned to the scheduler is inherited by whatever runs there next.
	done := make(chan outcome, 1)

	go func() {
		runtime.LockOSThread()

		err := Pin(1)
		if err != nil {
			done <- outcome{err: err}

			return
		}

		var got unix.CPUSet

		err = unix.SchedGetaffinity(0, &got)
		if err != nil {
			done <- outcome{err: err}

			return
		}

		// Forked from this thread, which is what a step is.
		out, cerr := exec.Command("sh", "-c",
			"grep Cpus_allowed_list /proc/self/status").Output()
		if cerr != nil {
			done <- outcome{err: cerr}

			return
		}

		done <- outcome{mask: got, child: strings.TrimSpace(string(out))}
	}()

	res := <-done
	if res.err != nil {
		t.Fatalf("pinning a locked thread: %v", res.err)
	}

	if res.mask.Count() != 1 || !res.mask.IsSet(1) {
		t.Errorf("a pinned thread may run on %d CPUs, want only CPU 1",
			res.mask.Count())
	}

	if !strings.HasSuffix(res.child, "\t1") && !strings.HasSuffix(res.child, " 1") {
		t.Errorf("a process forked from a pinned thread reports %q,"+
			"\n  want only CPU 1 - a step inherits the tracer's CPU the same way"+
			"\n  it inherits the tracer's filter, and nothing else pins it", res.child)
	}
}
