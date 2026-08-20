package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

type failingExec struct{ code int }

func (f failingExec) Run(_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID) (core.Result, error) {
	return core.Result{
		Layer: n.ID(), Captured: true, Exit: f.code,
		Output: "sh: echo produced: not found",
	}, nil
}

// A step that exits non-zero fails the build.
//
// It ran, so it is a result rather than an executor error - but a build whose
// commands failed has not succeeded, and reporting otherwise means a red build
// that looks green. The engine recorded the exit code, cached the result, and
// returned success, which is the worst of the three available behaviours.
func TestNonZeroExitFailsTheBuild(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{"false"}},
		Meta: ir.Meta{Source: at(5)},
	}

	cache := newMemCache()

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: failingExec{code: 127},
		Cache:    cache,
		Blobs:    allBlobs{},
		Writer:   testStep,
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err == nil {
		t.Fatal("a step exiting 127 did not fail the build")
	}

	// The diagnostic must carry the exit code, the location, and what the step
	// said - an exit code alone sends the reader back to run it by hand.
	for _, want := range []string{"127", at(5), "not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}

	// And a failure must not be cached. Caching it would make the next build
	// fail identically without running anything, so fixing the cause would
	// appear to change nothing.
	if cache.len() != 0 {
		t.Errorf("a failed step published %d cache entries, want 0", cache.len())
	}
}
