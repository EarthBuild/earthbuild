package core

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestATranslatorCompetesWithANativeMachine.
//
// **The hundred-fold argument is about interpreters, and Rosetta is not one.**
// Placement applies emulation as a second pass, considered only when nothing
// can run a step natively, and the reason given is that "emulated work runs on
// the order of a hundred times slower - every instruction through an
// interpreter". That is right for qemu and wrong for a translator that compiles
// ahead of time and caches the result.
//
// Measured on 64 amd64 steps: 95.90s on an arm64 Mac through Rosetta against
// 95.47s native on an x86 box. So a Mac was ruled ineligible for every step of
// an amd64 build while being, to within half a percent, as fast as the machine
// that got them - and a fleet of the two substituted one for the other instead
// of adding them: `64 delegated, 0 local` (E-F1).
func TestATranslatorCompetesWithANativeMachine(t *testing.T) {
	t.Parallel()

	amd64 := ir.Platform{OS: "linux", Arch: "amd64"}
	arm64 := ir.Platform{OS: "linux", Arch: "arm64"}

	native := Worker{ID: "box", Platform: amd64}

	mac := Worker{
		ID: "mac", Platform: arm64, IsInvoker: true,
		Translates: []ir.Platform{amd64},
	}

	slow := Worker{ID: "qemu-box", Platform: arm64, Emulates: []ir.Platform{amd64}}

	n := &ir.Node{Platform: amd64}

	if !eligibleFor(n, native, arm64) {
		t.Fatal("a machine of the step's own architecture was refused")
	}

	if !eligibleFor(n, mac, arm64) {
		t.Error("a machine that translates the step's architecture was refused," +
			" so it cannot join an amd64 build at all and a fleet of one Mac and" +
			" one x86 box can only move work, never share it")
	}

	// **The rule that was right stays right.** An interpreter must not take
	// work from a busy native machine, whatever the load: a hundredfold is not
	// a queue anybody can be deep enough to beat.
	if eligibleFor(n, slow, arm64) {
		t.Error("a machine that interprets the step's architecture was made" +
			" eligible beside a native one")
	}
}

// TestAnInterpreterIsStillTheLastResort. With nothing native and nothing
// translating, an interpreter is better than refusing to build.
func TestAnInterpreterIsStillTheLastResort(t *testing.T) {
	t.Parallel()

	amd64 := ir.Platform{OS: "linux", Arch: "amd64"}
	arm64 := ir.Platform{OS: "linux", Arch: "arm64"}

	slow := Worker{ID: "qemu-box", Platform: arm64, Emulates: []ir.Platform{amd64}}

	s := &Scheduler{Workers: []Worker{{ID: "mac", Platform: arm64, IsInvoker: true}, slow}}

	got, err := s.place(&ir.Node{Platform: amd64}, map[string]int{}, nil)
	if err != nil {
		t.Fatalf("a step nothing can run natively was placed nowhere: %v", err)
	}

	if got.ID != "qemu-box" {
		t.Errorf("placed on %q, want the only machine that can run it", got.ID)
	}
}
