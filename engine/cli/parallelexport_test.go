package cli

import (
	"runtime"
	"testing"
)

// TestHowManyArtifactsAreWrittenAtOnce.
//
// **Off unless asked, because it changes what a failing build leaves behind.**
// Written one at a time, an artifact after a failure is never written; written
// several at a time, one already in flight can land before the cancel reaches
// it. The error is the same error and nothing but the person running the build
// reads those files - but it is a behaviour change, and the switch is what makes
// it one somebody chose.
func TestHowManyArtifactsAreWrittenAtOnce(t *testing.T) {
	for _, c := range []struct {
		set  string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"false", 0},
		{"no", 0},
		{"-3", 0},
		{"1", 1},
		{"4", 4},
		{"64", 64},
		{"yes", min(runtime.NumCPU(), 8)},
		{"true", min(runtime.NumCPU(), 8)},
	} {
		t.Run(c.set, func(t *testing.T) {
			t.Setenv(EnvParallelExport, c.set)

			if got := exportWidth(); got != c.want {
				t.Errorf("%s=%q gives width %d, want %d",
					EnvParallelExport, c.set, got, c.want)
			}
		})
	}
}

// TestTheExportWidthIsBoundedWhateverTheMachine.
//
// **Past eight the unmounts are queueing, not working.** Thirty-two overlay
// unmounts take 87ms one at a time and 36ms sixteen at a time - 2.4x, because
// the kernel holds `namespace_sem` for write through each one. Goroutines past
// the point where that lock saturates only make the queue longer, so a machine
// with ninety-six cores does not ask for ninety-six exports (E817).
func TestTheExportWidthIsBoundedWhateverTheMachine(t *testing.T) {
	t.Setenv(EnvParallelExport, "yes")

	if got := exportWidth(); got > 8 {
		t.Errorf("width %d on a %d-core machine, want at most 8"+
			"\n  the mount lock is the limit, not the core count", got, runtime.NumCPU())
	}
}
