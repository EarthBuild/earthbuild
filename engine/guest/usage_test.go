package guest

import (
	"os/exec"
	"testing"
	"time"
)

// A finished process reports what it spent.
//
// The numbers come from the kernel at wait, and by the time a result reaches the
// host the process is gone - so this is measured where the process is, and
// nowhere else can it be (E467).
func TestAFinishedProcessReportsWhatItSpent(t *testing.T) {
	t.Parallel()

	// Something that costs measurable CPU rather than sleeping: a sleep spends
	// wall time and no CPU at all, which is the number this would then be
	// asserting nothing about.
	cmd := exec.Command("/bin/sh", "-c", "i=0; while [ $i -lt 40000 ]; do i=$((i+1)); done")

	err := cmd.Run()
	if err != nil {
		t.Fatalf("the probe command did not run: %v", err)
	}

	cpu, mem := usageOf(cmd.ProcessState)

	if cpu <= 0 {
		t.Errorf("a loop of forty thousand iterations reported %v of CPU", cpu)
	}

	// Memory is Linux-only: `ru_maxrss` is kilobytes there and bytes on darwin,
	// and a number this platform cannot state honestly is left at zero rather
	// than converted with a guess.
	if mem == 0 && isLinux {
		t.Error("a process that ran reported no peak memory at all")
	}

	if mem != 0 && mem < 64*1024 {
		t.Errorf("peak memory is %d bytes, which is smaller than any process"+
			"\n  the units are probably kilobytes read as bytes", mem)
	}
}

// A state from a process that never started reports nothing.
//
// Nothing rather than a made-up number: a build that says it used no memory is
// wrong in a way somebody can see, and one that invents a plausible figure is
// not.
func TestAProcessThatNeverRanReportsNothing(t *testing.T) {
	t.Parallel()

	cpu, mem := usageOf(nil)
	if cpu != 0 || mem != 0 {
		t.Errorf("a process that never ran reported %v and %d bytes", cpu, mem)
	}
}

var _ = time.Second
