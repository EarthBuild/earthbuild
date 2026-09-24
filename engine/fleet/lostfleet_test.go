package fleet_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A fleet that empties mid-build is reported, once.
//
// E255 made the *startup* case loud: a driver whose workers never arrived says
// so, because a fleet build that silently became a local one is
// indistinguishable from a slow fleet. Eviction (E256) created the same
// situation later - the workers arrive, and then the machines go away - and the
// build fell back to local without a word.
//
// Once, not per step. A build with five hundred delegable steps would print five
// hundred identical lines, which is how a message stops being read.
func TestAFleetThatEmptiesMidBuildIsSaidOnce(t *testing.T) {
	t.Parallel()

	var said []string

	local := &countingLocal{}

	d := &fleet.Delegating{
		Local: local,
		// A fleet with nobody in it, which is what eviction leaves behind.
		Fleet: &fleet.InProcess{},
		Note:  func(s string) { said = append(said, s) },
	}

	for range 3 {
		_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
		if err != nil {
			t.Fatalf("a fleet with no workers must not fail the build: %v", err)
		}
	}

	if local.runs != 3 {
		t.Errorf("%d step(s) ran locally, want 3", local.runs)
	}

	if len(said) != 1 {
		t.Fatalf("said %d time(s) over three steps, want 1"+
			"\n  %q\n  a line per step is how a message stops being read",
			len(said), said)
	}

	// Naming the step is what makes it actionable: the first delegable step to
	// fall back is where the fleet was last expected to be there.
	if !strings.Contains(said[0], "Earthfile:3") {
		t.Errorf("said %q, which does not say where the build noticed", said[0])
	}
}

// A step that could never be delegated is not a fleet that has gone.
//
// A secret, a cache mount, a docker daemon: these run locally by design (E230),
// on a fleet that is in perfect health. Reporting them as a lost fleet would cry
// wolf on every build that has a `RUN --secret` in it, and the message that
// matters - the fleet is gone - would be the one nobody believed.
func TestAStepThatCouldNeverBeDelegatedIsNotALostFleet(t *testing.T) {
	t.Parallel()

	var said []string

	local := &countingLocal{}
	f := &fleet.InProcess{}

	f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{Version: fleet.Version, Layer: ir.NodeID{2}}, nil
	})

	d := &fleet.Delegating{
		Local: local,
		Fleet: f,
		Note:  func(s string) { said = append(said, s) },
	}

	n := &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{"x"}, Interactive: true},
		Meta: ir.Meta{Source: "Earthfile:9"},
	}

	_, err := d.Run(t.Context(), n, core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if local.runs != 1 {
		t.Errorf("%d step(s) ran locally, want 1", local.runs)
	}

	if len(said) != 0 {
		t.Errorf("said %q about a step that was never delegable"+
			"\n  every build with a secret in it would carry this, and the"+
			" message that matters would be the one nobody believed", said)
	}
}

// Reporting is optional, and its absence is not a crash.
//
// `Delegating` is constructed in tests and by callers that have nowhere to put a
// line. A nil Note has to mean nobody is listening, not a nil call.
func TestADelegatingWithNobodyListeningStillBuilds(t *testing.T) {
	t.Parallel()

	local := &countingLocal{}
	d := &fleet.Delegating{Local: local, Fleet: &fleet.InProcess{}}

	_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if local.runs != 1 {
		t.Errorf("%d step(s) ran locally, want 1", local.runs)
	}
}

// A worker's refusal is said out loud, once.
//
// **A refusal that is silently absorbed looks exactly like a fleet nobody is
// using**, and the reason is the only thing that tells the two apart. Two
// machines reported "4 step(s) delegated, 4 here" for an afternoon and the
// message that explained it was being discarded three lines from where it
// arrived (E308).
//
// Once, because five hundred delegable steps refused for one reason would print
// five hundred identical lines - and the step is named because a refusal is
// often about *that* step rather than about the fleet.
func TestAWorkersRefusalIsSaidOutLoudOnce(t *testing.T) {
	t.Parallel()

	var said []string

	f := &fleet.InProcess{}

	f.AddWorker(func(context.Context, fleet.Assignment) (fleet.Reply, error) {
		return fleet.Reply{
			Version: fleet.Version,
			Refused: "1 of 1 input(s) could not be fetched",
		}, nil
	})

	d := &fleet.Delegating{
		Local: &countingLocal{},
		Fleet: f,
		Note:  func(s string) { said = append(said, s) },
	}

	for range 3 {
		_, err := d.Run(t.Context(), delegable(), core.Worker{ID: "w1"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(said) != 1 {
		t.Fatalf("said %d time(s) over three refusals: %q", len(said), said)
	}

	if !strings.Contains(said[0], "could not be fetched") {
		t.Errorf("said %q, which does not carry the worker's reason"+
			"\n  without it a refused fleet and an unused one look the same",
			said[0])
	}

	if !strings.Contains(said[0], "Earthfile:3") {
		t.Errorf("said %q, which does not say which step", said[0])
	}
}
