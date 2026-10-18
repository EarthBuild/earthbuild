package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// emulationStep is a step wanting one architecture, for a fleet that may or may
// not have a machine of it.
func emulationStep(p ir.Platform) *ir.Node {
	return &ir.Node{
		Op:       ir.Op{Kind: ir.OpExec, Args: []string{"emulated"}},
		Platform: p,
	}
}

// placeWith runs one step and says where it landed, or "" if the build refused.
func placeWith(t *testing.T, workers []core.Worker, n *ir.Node) (string, error) {
	t.Helper()

	e := &placingExec{}
	s := &core.Scheduler{
		Workers: workers, Executor: e, Cache: newMemCache(), Blobs: allBlobs{},
		Writer: testStep, Record: &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		return "", err
	}

	return e.where("emulated"), nil
}

// **Emulation is a fallback, never a preference.** A machine of the right
// architecture runs the step; one that can only emulate it runs the step when
// there is no such machine. The other way round is a build that is slower for
// no reason, and whose placement turns on load rather than on what the machines
// are.
func TestAnEmulatingWorkerLosesToANativeOne(t *testing.T) {
	t.Parallel()

	where, err := placeWith(t, []core.Worker{
		{ID: "emu", Platform: amd64, IsInvoker: true, Emulates: []ir.Platform{arm64}},
		{ID: "native", Platform: arm64, IsInvoker: true},
	}, emulationStep(arm64))
	if err != nil {
		t.Fatal(err)
	}

	if where != "native" {
		t.Errorf("placed on %q, want the machine that is actually arm64", where)
	}
}

// With nobody of that architecture, an emulator is what makes the build
// possible at all, which is the whole reason to have one.
func TestAStepGoesToAnEmulatorWhenNothingElseRunsIt(t *testing.T) {
	t.Parallel()

	where, err := placeWith(t, []core.Worker{
		{ID: "emu", Platform: amd64, IsInvoker: true, Emulates: []ir.Platform{arm64}},
	}, emulationStep(arm64))
	if err != nil {
		t.Fatal(err)
	}

	if where != "emu" {
		t.Errorf("placed on %q, want the emulator", where)
	}
}

// A machine that cannot emulate the architecture is still refused, and the
// refusal still names emulation - a reader with one machine and no binfmt needs
// to know that is the missing part.
func TestWithoutEmulationTheRefusalStandsAndSaysSo(t *testing.T) {
	t.Parallel()

	_, err := placeWith(t, []core.Worker{
		{ID: "only", Platform: amd64, IsInvoker: true},
	}, emulationStep(arm64))
	if err == nil {
		t.Fatal("the step was placed on a machine that cannot run it")
	}

	if !strings.Contains(err.Error(), "emulation") {
		t.Errorf("the refusal does not mention emulation: %v", err)
	}
}
