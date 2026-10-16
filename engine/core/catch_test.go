package core_test

import (
	"context"
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// catchGraph builds `TRY / RUN <cmd> / CATCH / RUN handle / END`.
//
// The handler stands on the guarded step, because that is where CATCH runs -
// in the build environment the failure left behind, which is the only place
// worth inspecting after one.
func catchGraph(cmd string) (root, handler *ir.Node) {
	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}
	tried := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{cmd}, Tolerate: true},
		Inputs: []*ir.Node{img},
		Meta:   ir.Meta{Source: at(2), Description: "RUN " + cmd},
	}
	handler = &ir.Node{
		Op:        ir.Op{Kind: ir.OpExec, Args: []string{"handle"}},
		Inputs:    []*ir.Node{tried},
		OnFailure: tried,
		Meta:      ir.Meta{Source: at(4), Description: "RUN handle"},
	}

	return tried, handler
}

func runCatch(t *testing.T, root, handler *ir.Node) (*triedExec, error) {
	t.Helper()

	e := &triedExec{}
	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: root, Also: []*ir.Node{handler}})

	return e, err
}

// A handler runs when the step it guards failed.
func TestACatchHandlerRunsOnFailure(t *testing.T) {
	t.Parallel()

	root, handler := catchGraph(testFailure)

	e, err := runCatch(t, root, handler)
	if err == nil {
		t.Fatal("a build whose TRY failed reported success")
	}

	if !slices.Contains(e.ran, at(4)) {
		t.Errorf("the handler never ran: %q", e.ran)
	}
}

// And does not run when it did not.
//
// This is the whole difference between CATCH and an ordinary step, and getting
// it wrong runs recovery commands over a build that succeeded - the opposite of
// what was written.
func TestACatchHandlerIsSkippedOnSuccess(t *testing.T) {
	t.Parallel()

	root, handler := catchGraph("this passes")

	e, err := runCatch(t, root, handler)
	if err != nil {
		t.Fatalf("a build that succeeded reported failure: %v", err)
	}

	if slices.Contains(e.ran, at(4)) {
		t.Errorf("the handler ran over a build that did not fail: %q", e.ran)
	}
}

// A step standing on a skipped one is skipped too.
//
// A handler is usually several commands, and the second stands on the first.
// Running it against the guarded step's filesystem instead - the only other
// thing it could stand on - would execute half a recovery over a build that
// never went wrong.
func TestWhatStandsOnASkippedStepIsSkipped(t *testing.T) {
	t.Parallel()

	root, handler := catchGraph("this passes")

	second := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"handle more"}},
		Inputs: []*ir.Node{handler},
		Meta:   ir.Meta{Source: at(5), Description: "RUN handle more"},
	}

	e, err := runCatch(t, root, second)
	if err != nil {
		t.Fatalf("a build that succeeded reported failure: %v", err)
	}

	for _, src := range []string{at(4), at(5)} {
		if slices.Contains(e.ran, src) {
			t.Errorf("%s ran over a build that did not fail: %q", src, e.ran)
		}
	}
}

// Whether a step is a handler is not part of its identity.
//
// It decides *whether* the step runs, never what it computes - the same
// distinction `After` is on the same side of. A handler that keyed differently
// from the identical command written outside a TRY would miss a cache entry it
// is entitled to.
func TestBeingAHandlerDoesNotChangeIdentity(t *testing.T) {
	t.Parallel()

	_, handler := catchGraph(testFailure)

	plain := &ir.Node{
		Op:     handler.Op,
		Inputs: handler.Inputs,
		Meta:   handler.Meta,
	}

	if handler.ID() != plain.ID() {
		t.Error("a handler and the same command outside a TRY have different keys")
	}
}
