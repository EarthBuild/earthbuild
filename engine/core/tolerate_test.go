package core_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// triedExec fails the step whose command says so, and keeps its layer.
//
// Keeping the layer is the point rather than a convenience: `TRY / RUN test >
// report; FINALLY / SAVE ARTIFACT report` only means anything if the failed
// step's filesystem survives it.
type triedExec struct {
	mu  sync.Mutex
	ran []string
}

func (e *triedExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.mu.Lock()
	e.ran = append(e.ran, n.Meta.Source)
	e.mu.Unlock()

	if strings.Contains(strings.Join(n.Op.Args, " "), "fails") {
		return core.Result{Layer: n.ID(), Exit: 1, Output: "it failed\n", Captured: true}, nil
	}

	return core.Result{Layer: n.ID(), Captured: true}, nil
}

// A tolerated step that fails does not stop the build there, and the build
// still fails.
//
// Both halves matter. Stopping there would mean FINALLY never runs, which is
// the entire reason TRY exists; not failing at the end would mean a red test
// suite reports a green build, which is worse than either.
func TestAToleratedFailureRunsWhatFollowsAndStillFails(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}
	tried := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{testFailure}, Tolerate: true},
		Inputs: []*ir.Node{img},
		Meta:   ir.Meta{Source: at(2)},
	}
	// FINALLY stands on the failed step: that is where its artifact is.
	finally := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"save the report"}},
		Inputs: []*ir.Node{tried},
		Meta:   ir.Meta{Source: at(4)},
	}

	e := &triedExec{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: finally})
	if err == nil {
		t.Fatal("a build whose TRY failed reported success")
	}

	for _, want := range []string{at(2), "it failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not mention %q:\n%s", want, err)
		}
	}

	// FINALLY ran, which it could not have done if the failure stopped there.
	var sawFinally bool

	for _, src := range e.ran {
		if src == at(4) {
			sawFinally = true
		}
	}

	if !sawFinally {
		t.Errorf("the step after the tolerated failure never ran: %v", e.ran)
	}
}

// A tolerated step that succeeds is an ordinary step.
func TestAToleratedStepThatPassesIsOrdinary(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}
	tried := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"this passes"}, Tolerate: true},
		Inputs: []*ir.Node{img},
		Meta:   ir.Meta{Source: at(2)},
	}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: &triedExec{},
		Blobs:    allBlobs{},
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: tried})
	if err != nil {
		t.Fatalf("a tolerated step that succeeded failed the build: %v", err)
	}
}

// An untolerated failure still stops the build immediately.
func TestAnUntoleratedFailureStillStopsTheBuild(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}
	tried := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{testFailure}},
		Inputs: []*ir.Node{img},
		Meta:   ir.Meta{Source: at(2)},
	}
	after := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"never reached"}},
		Inputs: []*ir.Node{tried},
		Meta:   ir.Meta{Source: at(3)},
	}

	e := &triedExec{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Blobs:    allBlobs{},
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: after})
	if err == nil {
		t.Fatal("a failing step did not stop the build")
	}

	for _, src := range e.ran {
		if src == at(3) {
			t.Error("the step after an untolerated failure ran anyway")
		}
	}
}
