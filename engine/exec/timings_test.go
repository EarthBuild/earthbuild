package exec

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/timing"
)

// A build that is slow should be able to say where it was slow.
//
// Locating the per-step cost took four benchmark designs and a CPU sample of the
// VM, because the engine could not be asked (E528). Every phase here is a round
// trip the host waits out, so timing it from this side measures the guest
// without instrumenting it.
func TestAPhaseReportsItsNameDurationAndStep(t *testing.T) { //nolint:paralleltest // swaps a package-level sink
	var out strings.Builder

	restore := timing.To
	timing.To = &out

	defer func() { timing.To = restore }()

	phase("materialise", "./Earthfile:4")()

	got := out.String()
	for _, want := range []string{"materialise", "./Earthfile:4", "s"} {
		if !strings.Contains(got, want) {
			t.Errorf("a timing line without %q: %q", want, got)
		}
	}
}

// Off by default, and off means free: the closure is what a caller keeps, so it
// has to be safe to call when nothing is being timed.
func TestTimingsAreSilentUnlessAskedFor(t *testing.T) { //nolint:paralleltest // ditto
	var out strings.Builder

	restore := timing.To
	timing.To = nil

	defer func() { timing.To = restore }()

	phase("materialise", "./Earthfile:4")()

	if out.Len() != 0 {
		t.Errorf("timings were reported without being asked for: %q", out.String())
	}
}
