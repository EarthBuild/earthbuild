//go:build darwin

package exec_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A VM booted ahead of the plan is the VM the plan then uses.
//
// Booting takes about 850ms and needs nothing the Earthfile says: the sandbox
// image is this engine's, not the build's, so its identity is known before a
// line is parsed. Planning meanwhile costs a registry round trip. Run one after
// the other and a build pays for both; run them together and it pays for the
// longer (E537).
//
// The property that makes it safe is this one: a prewarmed VM must be *reused*,
// never booted a second time, or the optimisation is a way to run two machines.
func TestAPrewarmedSandboxIsTheOneTheBuildUses(t *testing.T) { //nolint:paralleltest // boots a VM
	sb := exec.NewApple()
	sb.GuestBinary = buildGuestd(t)

	if err := sb.Available(); err != nil {
		t.Skipf("apple container backend unavailable: %v", err)
	}

	defer func() { _ = sb.Remove() }()

	sb.Prewarm(context.Background())

	if got := sb.Boots(); got != 1 {
		t.Fatalf("a prewarm booted %d VMs, want 1", got)
	}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	base := putProbeLayerAt(t, sb.StoreDir())
	n := guestStep("after-prewarm", "/probe")
	n.Inputs = []*ir.Node{base}

	if _, err := e.Run(context.Background(), n, core.Worker{ID: "vm"}, []ir.NodeID{base.ID()}, nil); err != nil {
		t.Fatalf("step: %v", err)
	}

	if got := sb.Boots(); got != 1 {
		t.Errorf("%d VMs booted in total, want the prewarmed one to have been used", got)
	}
}

// A prewarm on a machine with no backend is quiet.
//
// It is an optimisation, and an optimisation that fails is a build that is
// slower rather than a build that stops: whatever is wrong will be reported
// properly by the start that follows, with the context that start has.
func TestAPrewarmThatCannotWorkIsQuiet(t *testing.T) { //nolint:paralleltest // ditto
	sb := exec.NewApple()
	sb.GuestBinary = "/nonexistent/earth-guestd"

	sb.Prewarm(context.Background())

	if got := sb.Boots(); got != 0 {
		t.Errorf("%d VMs booted for a sandbox that cannot start", got)
	}
}
