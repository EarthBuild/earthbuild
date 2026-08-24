package cli

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A backend that cannot isolate says so before it boots a machine.
//
// The executor already refuses (E391), which is correct and late: on the macOS
// backend that refusal arrives after a VM with a docker daemon in it has been
// chosen, started and had a step sent to it. The author waits for a boot to be
// told about a flag.
//
// Knowable earlier - it is a property of the plan, which exists before any
// machine does - so it is checked there. Two checks at two boundaries, on the
// same argument as the scheduler's cache gate (E384): the later one is the
// guarantee, the earlier one is the courtesy, and they read different things.
func TestAPlanThatCannotBeIsolatedIsRefusedBeforeAnyMachineStarts(t *testing.T) {
	t.Parallel()

	iso := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"docker"}, Docker: true, IsolateDocker: true}}

	err := checkIsolationSupported(&ir.Graph{Root: iso})

	if !backendCanIsolate() {
		if err == nil {
			t.Fatal("a plan asking for a daemon per step was accepted by a backend" +
				" that will refuse it later, after a machine has been started")
		}

		if !strings.Contains(err.Error(), "--isolate") {
			t.Errorf("the refusal does not name the flag: %v", err)
		}

		return
	}

	if err != nil {
		t.Errorf("a backend that can isolate refused a plan anyway: %v", err)
	}
}

// A plan that asks for nothing unusual is never refused by this check.
//
// The failure worth guarding: a check that fires on every WITH DOCKER block
// would take the whole construct away from the backend that has supported it
// longest.
func TestAnOrdinaryDockerPlanIsNotRefused(t *testing.T) {
	t.Parallel()

	plain := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"docker"}, Docker: true}}

	err := checkIsolationSupported(&ir.Graph{Root: plain})
	if err != nil {
		t.Errorf("an ordinary WITH DOCKER block was refused: %v", err)
	}
}

// And the check runs, which is the half a unit test of the checker cannot show.
//
// *A mechanism that is not running and one that found nothing produce the same
// output* - the most recorded failure in this project - so the assertion goes
// through `executorFor`, the function that would otherwise choose an image and
// boot a machine. On a backend that cannot isolate, it must come back with the
// refusal and no machine.
func TestTheEarlyRefusalIsActuallyWired(t *testing.T) {
	t.Parallel()

	if backendCanIsolate() {
		t.Skip("this backend can isolate, so there is nothing here to refuse")
	}

	iso := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"docker"}, Docker: true, IsolateDocker: true}}

	var g engine

	_, err := g.executorFor(&interp.Plan{Graph: &ir.Graph{Root: iso}})
	if err == nil {
		t.Fatal("a plan this backend cannot run was accepted, and a machine was" +
			" started for it")
	}

	if !strings.Contains(err.Error(), "--isolate") {
		t.Errorf("the refusal that came back is about something else: %v", err)
	}
}
