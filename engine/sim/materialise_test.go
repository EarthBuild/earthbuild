package sim_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/coretest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

// TestSimulatedMaterialiserConforms runs the port's contract against the fake.
// The real implementations - overlayfs, and the macOS guest agent - run the
// same suite, which is the point of writing it before either exists.
func TestSimulatedMaterialiserConforms(t *testing.T) {
	t.Parallel()

	coretest.MaterialiserSuite(t, func(t *testing.T) (core.Materialiser, func()) {
		return &sim.Materialiser{}, func() {}
	})
}

// TestHandleLeaksAreVisible: a scheduler that forgets to release handles leaks
// mounts on a real implementation. The fake counts them so that leak is caught
// here, cheaply, rather than when a mount table fills.
func TestHandleLeaksAreVisible(t *testing.T) {
	t.Parallel()

	m := &sim.Materialiser{}

	h, err := m.Materialise(context.Background(), []ir.NodeID{{1}, {2}})
	if err != nil {
		t.Fatal(err)
	}

	if m.Outstanding() != 1 {
		t.Fatalf("outstanding = %d, want 1", m.Outstanding())
	}

	err = h.Release()
	if err != nil {
		t.Fatal(err)
	}

	if m.Outstanding() != 0 {
		t.Errorf("outstanding = %d after release, want 0", m.Outstanding())
	}
}
