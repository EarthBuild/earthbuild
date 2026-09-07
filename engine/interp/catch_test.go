package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

const trySrc = `
main:
    FROM alpine:3.22
    TRY
        RUN run-the-tests
    CATCH
        RUN collect-the-logs
    FINALLY
        RUN save-the-report
    END
    RUN carry-on
`

// A CATCH body is planned, and every step in it is conditional on the guarded
// step having failed.
func TestCatchIsPlannedAsAHandler(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(tryVersioned+trySrc, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var handler, tried *ir.Node

	for _, n := range p.Graph.Nodes() {
		switch n.Meta.Description {
		case "RUN collect-the-logs":
			handler = n
		case "RUN run-the-tests":
			tried = n
		}
	}

	if tried == nil {
		t.Fatalf("the guarded step is not in the graph:\n%s", describe(p.Graph.Nodes()))
	}

	if handler == nil {
		t.Fatalf("the CATCH body is not in the graph:\n%s", describe(p.Graph.Nodes()))
	}

	if handler.OnFailure == nil {
		t.Fatal("the handler is an ordinary step: it would run over a build that succeeded")
	}

	if handler.OnFailure.ID() != tried.ID() {
		t.Error("the handler is conditional on something other than the step it guards")
	}

	// It runs where the failure left things, which is the only place worth
	// inspecting after one.
	if len(handler.Inputs) == 0 || handler.Inputs[0].ID() != tried.ID() {
		t.Error("the handler does not stand on the failed step's filesystem")
	}
}

// What follows END continues from the TRY, not from the handler.
//
// A CATCH is a side branch: the build carries on from where it got to, and
// threading it through the handler would make every later step wait for
// commands that usually do not run at all.
func TestWhatFollowsEndDoesNotStandOnTheHandler(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(tryVersioned+trySrc, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description != "RUN carry-on" {
			continue
		}

		if reaches(n, "RUN collect-the-logs") {
			t.Error("the rest of the build stands on the CATCH body")
		}

		if !reaches(n, "RUN save-the-report") {
			t.Error("the rest of the build does not follow FINALLY")
		}
	}
}

// The handler is still scheduled, despite nothing standing on it.
func TestTheHandlerIsReachableFromTheGraph(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(tryVersioned+trySrc, testMain)
	if err != nil {
		t.Fatal(err)
	}

	var found bool

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description == "RUN collect-the-logs" {
			found = true
		}
	}

	if !found {
		t.Error("the CATCH body would never be scheduled")
	}
}

// A CATCH with several commands chains, and only the first names the guarded
// step - the rest are skipped by standing on one that was.
func TestAMultiCommandHandlerChains(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(tryVersioned+`
main:
    FROM alpine:3.22
    TRY
        RUN run-the-tests
    CATCH
        RUN collect-the-logs
        RUN upload-the-logs
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.Meta.Description != "RUN upload-the-logs" {
			continue
		}

		if !reaches(n, "RUN collect-the-logs") {
			t.Errorf("the second handler command does not follow the first:\n%s",
				describe(p.Graph.Nodes()))
		}

		return
	}

	t.Errorf("the second handler command is not in the graph:\n%s", describe(p.Graph.Nodes()))
}

// A TRY with no CATCH is unchanged.
func TestATryWithoutCatchHasNoHandler(t *testing.T) {
	t.Parallel()

	p, err := interp.Build(tryVersioned+`
main:
    FROM alpine:3.22
    TRY
        RUN run-the-tests
    FINALLY
        RUN save-the-report
    END
`, testMain)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range p.Graph.Nodes() {
		if n.OnFailure != nil {
			t.Errorf("%s is conditional in a TRY that has no CATCH", n.Meta.Description)
		}
	}

	if !strings.Contains(describe(p.Graph.Nodes()), "save-the-report") {
		t.Error("FINALLY stopped working")
	}
}
