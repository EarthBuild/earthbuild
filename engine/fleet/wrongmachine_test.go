package fleet

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A worker refuses a step for another machine, and not one for its own.
//
// The refusal is the safety net under placement: the driver should not send a
// step to a machine that cannot build it, and when the inventory and the worker
// disagree the worker is the party that knows (I10, E267). Building it anyway
// succeeds and produces binaries for the wrong machine, which is the failure
// with no symptom until somebody runs them.
//
// It compared the two names as strings, so a worker reporting `linux/arm64` -
// which is what `runtime.GOOS/GOARCH` gives, there being no variant to report -
// refused a step written `--platform=linux/arm64/v8`. The fifth copy of one
// comparison, found by grepping for the rule rather than for the symptom (E952).
func TestAWorkerRefusesAnotherMachineAndNotItsOwn(t *testing.T) {
	t.Parallel()

	arm64 := ir.Platform{OS: "linux", Arch: "arm64"}

	for _, tc := range []struct {
		name    string
		worker  ir.Platform
		step    string
		refused bool
	}{{
		name:   "a step that states the variant the worker does not",
		worker: arm64, step: "linux/arm64/v8",
	}, {
		name:   "the same platform",
		worker: arm64, step: "linux/arm64",
	}, {
		name:   "another architecture",
		worker: arm64, step: "linux/amd64", refused: true,
	}, {
		name:   "a variant both state and differ on",
		worker: ir.Platform{OS: "linux", Arch: "arm", Variant: "v6"},
		step:   "linux/arm/v7", refused: true,
	}, {
		// Neither side knowing is not a mismatch: an assignment with no
		// platform, or a worker that has not said what it is, is the
		// single-machine case and every test's case.
		name:   "the step names none",
		worker: arm64, step: "",
	}, {
		name: "the worker knows nothing about itself",
		step: "linux/amd64",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := wrongMachine(tc.worker, tc.step); got != tc.refused {
				t.Errorf("worker %s against step %q = %v, want %v",
					platformName(tc.worker), tc.step, got, tc.refused)
			}
		})
	}
}
