package core

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

var (
	arm = ir.Platform{OS: "linux", Arch: "arm64"}
	x86 = ir.Platform{OS: "linux", Arch: "amd64"}
)

// A step with no platform means "this machine's", not "anybody's".
//
// The rule was that an empty platform on a node matches every worker. On one
// machine that is true and harmless. On a **fleet of mixed architectures it is a
// wrong build**: a step written without a platform means native, and running it
// on the other architecture produces binaries for the wrong machine, filed under
// a key that says nothing about which (§4.7.1).
//
// The failure is silent in the worst way - the step succeeds, the layer is real,
// and what is wrong about it only appears when somebody runs it.
func TestAStepWithNoPlatformDoesNotCrossArchitectures(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc"}}}

	if eligibleFor(n, Worker{ID: "other", Platform: x86}, arm) {
		t.Error("a step with no platform was placed on the other architecture" +
			"\n  it means this machine's platform, and the layer would be for" +
			" a machine nobody asked about")
	}

	if !eligibleFor(n, Worker{ID: "same", Platform: arm}, arm) {
		t.Error("a step with no platform was refused a worker of the invoker's" +
			" own architecture")
	}
}

// A worker that has not said what it is gets nothing platform-specific.
//
// `Rendezvous.Inventory` named workers and left their platform zero, so every
// fleet worker claimed to be an unknown machine. Under the old rule that made
// them eligible for every step that named no platform - the mixed-architecture
// fault above - and ineligible for every step that did, so a `--platform` build
// silently never used the fleet at all. One missing field, two faults, in
// opposite directions.
//
// Unknown now means ineligible, which is the safe direction: a fleet whose
// workers have not announced themselves is unused rather than wrong.
func TestAWorkerThatHasNotSaidWhatItIsGetsNothing(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc"}}}

	if eligibleFor(n, Worker{ID: "silent"}, arm) {
		t.Error("a step was placed on a worker of unknown architecture" +
			"\n  refusing to guess costs a slower build; guessing costs a wrong" +
			" one")
	}
}

// An explicit platform still decides, and now the fleet can satisfy it.
func TestAnExplicitPlatformPicksTheMachineThatHasIt(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op:       ir.Op{Kind: ir.OpExec, Args: []string{"cc"}},
		Platform: x86,
	}

	if !eligibleFor(n, Worker{ID: "x86", Platform: x86}, arm) {
		t.Error("a --platform=linux/amd64 step was refused an amd64 worker" +
			"\n  cross-building is the reason to have a mixed fleet at all")
	}

	if eligibleFor(n, Worker{ID: "arm", Platform: arm}, arm) {
		t.Error("a --platform=linux/amd64 step was placed on arm64")
	}
}

// Nothing about a host step changes.
//
// It runs on the invoker whatever the platforms say, because it is the invoker's
// filesystem it touches (C.3).
func TestAHostStepStillRunsOnTheInvoker(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"ls"}}}

	if !eligibleFor(n, Worker{ID: "me", IsInvoker: true}, arm) {
		t.Error("a host step was refused the invoker")
	}

	if eligibleFor(n, Worker{ID: "them", Platform: arm}, arm) {
		t.Error("a host step was offered to a worker")
	}
}

// A fleet with no platforms anywhere still works.
//
// Every in-process fleet and every test builds workers without platforms, and so
// does a single-machine build before anybody has configured anything. When
// nothing knows its platform there is no mismatch to protect against, and a rule
// that refused would refuse every build on the way to protecting none.
func TestAFleetThatKnowsNoPlatformsStillPlaces(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc"}}}

	if !eligibleFor(n, Worker{ID: "w1"}, ir.Platform{}) {
		t.Error("a build where nothing declares a platform placed nothing")
	}

	// Including on a worker that *does* know what it is. The invoker not
	// knowing its own platform is the case here, and refusing every worker that
	// has announced one would make a fleet useless to exactly the driver that
	// cannot check it - which is refusing to build rather than refusing to
	// guess.
	if !eligibleFor(n, Worker{ID: "known", Platform: x86}, ir.Platform{}) {
		t.Error("a build whose invoker does not know its own platform refused" +
			" a worker that knows its own")
	}
}
