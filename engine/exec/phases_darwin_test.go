//go:build darwin

package exec_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestSandboxPhaseTimings breaks the fixed cost of a rebuild into its parts.
//
// E21 established that a rebuild which does anything at all pays about 215ms
// before the first step, and that a step costs 6ms at the margin - so the fixed
// cost is what is worth attacking and the per-step cost is not. About 130ms of
// it was unaccounted for, and "connecting to the guest, probably" is not a
// measurement.
//
// Kept in the repository rather than done once at a shell, because the number
// changes as the engine does and the cheapest way to be wrong about performance
// is to quote a figure from a fortnight ago.
//
// Not a pass/fail test: it asserts only that each phase completes. Timing on a
// developer's machine varies with what else is running, so a threshold here
// would fail for reasons that are nobody's fault.
func TestSandboxPhaseTimings(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_TIMINGS") == "" {
		t.Skip("set EARTH_TEST_TIMINGS=1 to measure the phases of a rebuild")
	}

	sb := exec.NewApple()
	sb.GuestBinary = buildGuestd(t)

	err := sb.Available()
	if err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()
	defer func() { _ = sb.Remove() }()

	base := putProbeLayerAt(t, sb.StoreDir())

	// The first step pays for connecting to the guest; the rest do not, which is
	// the whole distinction being measured.
	var phases []struct {
		name string
		d    time.Duration
	}

	for i, name := range []string{"first step (connect + run + capture)", "second step", "third step"} {
		n := guestStep(string(rune('a'+i)), "/probe")
		n.Inputs = []*ir.Node{base}

		start := time.Now()

		if _, err := e.Run(context.Background(), n, core.Worker{ID: "vm"},
			[]ir.NodeID{base.ID()}, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		phases = append(phases, struct {
			name string
			d    time.Duration
		}{name, time.Since(start)})
	}

	for _, p := range phases {
		t.Logf("%-40s %6.1fms", p.name, float64(p.d.Microseconds())/1000)
	}

	// The subtraction is the point: everything the first step paid that the
	// second did not is the cost of getting a guest to talk to - including, for
	// this executor, booting the VM.
	if len(phases) >= 2 {
		t.Logf("%-40s %6.1fms", "-> boot + connect (first ever build)",
			float64((phases[0].d-phases[1].d).Microseconds())/1000)
	}

	// The inner loop does not boot. A second executor over the same
	// configuration finds the VM this one left running, which is what every
	// rebuild after the first does, and its first step is the number that
	// matters.
	again := exec.NewApple()
	again.GuestBinary = sb.GuestBinary
	again.Store = sb.StoreDir()

	e2, err := exec.New(again)
	if err != nil {
		t.Fatal(err)
	}

	defer e2.Close()

	n := guestStep("reused", "/probe")
	n.Inputs = []*ir.Node{base}

	start := time.Now()

	if _, err := e2.Run(context.Background(), n, core.Worker{ID: "vm"},
		[]ir.NodeID{base.ID()}, nil); err != nil {
		t.Fatalf("reusing the VM: %v", err)
	}

	reused := time.Since(start)

	t.Logf("%-40s %6.1fms", "first step against a running VM", float64(reused.Microseconds())/1000)
	t.Logf("%-40s %6.1fms", "-> connect only (no boot)",
		float64((reused-phases[1].d).Microseconds())/1000)
}
