package cli

import (
	"runtime"
	"testing"
)

// A build runs as many steps at once as the *sandbox* has processors.
//
// **The host's core count is the wrong number when the steps run elsewhere.**
// A microVM is given four vCPUs and two gigabytes; the machine that starts it
// here has thirty-two cores, so one-step-per-core put thirty-two concurrent
// steps inside a four-vCPU guest - each unpacking layers and running a package
// manager in shared memory. What that produces is not a clean failure: it is a
// step that exits non-zero having printed nothing, which reads as the command
// being wrong.
func TestParallelismFollowsTheSandbox(t *testing.T) {
	t.Parallel()

	if got := parallelismFor(&fixedCPUs{n: 4}, func(string) string { return "" }); got != 4 {
		t.Errorf("a four-processor sandbox runs %d steps at once", got)
	}
}

// A sandbox that shares this machine's processors says nothing, and the
// scheduler's own default - one per core - is right for it.
func TestASandboxOnThisMachineKeepsTheDefault(t *testing.T) {
	t.Parallel()

	if got := parallelismFor(struct{}{}, func(string) string { return "" }); got != 0 {
		t.Errorf("a sandbox with no opinion asked for %d", got)
	}
}

// What the invoker asked for wins over both: the setting exists to make a build
// serial, and a sandbox overriding that would take away the instrument.
func TestTheSettingWinsOverTheSandbox(t *testing.T) {
	t.Parallel()

	got := parallelismFor(&fixedCPUs{n: 4}, func(string) string { return "1" })
	if got != 1 {
		t.Errorf("EARTH_PARALLELISM=1 gave %d", got)
	}
}

// A sandbox claiming more than this machine has is not believed: it would
// oversubscribe the processors actually doing the work.
func TestASandboxIsNotBelievedPastThisMachine(t *testing.T) {
	t.Parallel()

	got := parallelismFor(&fixedCPUs{n: runtime.NumCPU() * 4}, func(string) string { return "" })
	if got > runtime.NumCPU() {
		t.Errorf("a sandbox claiming %d processors got %d", runtime.NumCPU()*4, got)
	}
}

type fixedCPUs struct{ n int }

func (f *fixedCPUs) CPUs() int { return f.n }
