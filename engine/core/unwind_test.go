package core_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// unwindExec fails whichever step says it should, and records what ran.
type unwindExec struct {
	mu  sync.Mutex
	ran []string
}

func (e *unwindExec) Run(
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

func (e *unwindExec) did(source string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	return slices.Contains(e.ran, source)
}

// A handler runs when the step it guards fails, even though the build stops.
//
// This is what a teardown needs and what the engine could not express. An
// OnFailure edge says "run only if that step failed", which is exactly right -
// but the scheduler abandoned the build at the failure, so nothing guarded by
// it was ever reached. The only way to get a handler to run was TRY's
// tolerance, and tolerance has no end: everything downstream runs, including
// whatever follows the block that failed.
//
// So a WITH DOCKER block whose body failed left its containers running, holding
// their ports, for every build that came after it on that machine (E33). The
// handler is the fix; being able to run it is this.
//
// The build must still fail, and with the original error. A teardown that
// swallowed the failure would turn a broken build green, which is worse than
// the leak it cleans up.
func TestAHandlerRunsWhenItsStepFails(t *testing.T) {
	t.Parallel()

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Platform: amd64}

	failed := &ir.Node{
		Op:       ir.Op{Kind: ir.OpExec, Args: []string{testFailure}},
		Platform: amd64,
		Inputs:   []*ir.Node{base},
		Meta:     ir.Meta{Source: at(2)},
	}

	// The teardown: guarded on the step that failed, and standing on it,
	// because a teardown reads the environment the failure left behind.
	handler := &ir.Node{
		Op:        ir.Op{Kind: ir.OpExec, Args: []string{testCleanup}},
		Platform:  amd64,
		Inputs:    []*ir.Node{failed},
		OnFailure: failed,
		Meta:      ir.Meta{Source: at(3)},
	}

	exec := &unwindExec{}
	s := newSched(newMemCache(), allBlobs{}, exec)

	_, err := s.Run(context.Background(), &ir.Graph{Root: failed, Also: []*ir.Node{handler}})
	if err == nil {
		t.Fatal("the build succeeded despite a failing step")
	}

	if !strings.Contains(err.Error(), at(2)) {
		t.Errorf("the error does not name the step that failed:\n%v", err)
	}

	if !exec.did(at(3)) {
		t.Error("the handler did not run, so a teardown after a failure is still impossible")
	}
}

// A handler whose step succeeded does not run, which is what the guard means.
func TestAHandlerDoesNotRunWhenItsStepSucceeds(t *testing.T) {
	t.Parallel()

	base := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Platform: amd64}

	ok := &ir.Node{
		Op:       ir.Op{Kind: ir.OpExec, Args: []string{"this works"}},
		Platform: amd64,
		Inputs:   []*ir.Node{base},
		Meta:     ir.Meta{Source: at(2)},
	}

	handler := &ir.Node{
		Op:        ir.Op{Kind: ir.OpExec, Args: []string{testCleanup}},
		Platform:  amd64,
		Inputs:    []*ir.Node{ok},
		OnFailure: ok,
		Meta:      ir.Meta{Source: at(3)},
	}

	exec := &unwindExec{}
	s := newSched(newMemCache(), allBlobs{}, exec)

	_, err := s.Run(context.Background(), &ir.Graph{Root: ok, Also: []*ir.Node{handler}})
	if err != nil {
		t.Fatal(err)
	}

	if exec.did(at(3)) {
		t.Error("a handler ran for a step that did not fail")
	}
}
