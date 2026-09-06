package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// execFunc runs a step with a function, so a test can decide per node.
type execFunc func(context.Context, *ir.Node) (core.Result, error)

func (f execFunc) Run(
	ctx context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	return f(ctx, n)
}

// End to end: a build that fails tells every stopped step what stopped it.
//
// The unit tests above prove the wording; this proves the wiring - that the
// cause reaches the steps the scheduler cancels, through a real run, rather than
// only where a test hands it over directly.
func TestABuildTellsStoppedStepsWhatStoppedThem(t *testing.T) {
	t.Parallel()

	// One step fails at once; the other is slow enough to still be running when
	// the cancellation reaches it.
	root := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"img"}}, Meta: ir.Meta{Source: "Earthfile:1"}}

	quick := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"quick"}}, Inputs: []*ir.Node{root},
		Meta: ir.Meta{Source: "Earthfile:9"},
	}

	slow := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"slow"}}, Inputs: []*ir.Node{root},
		Meta: ir.Meta{Source: "Earthfile:20"},
	}

	top := &ir.Node{Op: ir.Op{Kind: ir.OpMerge}, Inputs: []*ir.Node{quick, slow}, Meta: ir.Meta{Source: "Earthfile:30"}}

	var stopped error

	// The slow step must already be running when the quick one fails, or the
	// scheduler simply never starts it and there is nothing to cancel.
	started := make(chan struct{})

	s := &core.Scheduler{
		Workers: []core.Worker{{ID: "w", IsInvoker: true}},
		Blobs:   allBlobs{},
		Executor: execFunc(func(ctx context.Context, n *ir.Node) (core.Result, error) {
			switch n.Op.Args[0] {
			case "quick":
				<-started

				return core.Result{}, &core.StepError{Source: n.Meta.Source, Desc: "RUN make", Exit: 1}
			case "slow":
				close(started)
				<-ctx.Done()

				stopped = ctx.Err()

				return core.Result{}, ctx.Err()
			}

			return core.Result{Layer: n.ID(), Captured: true}, nil
		}),
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: top})
	if err == nil {
		t.Fatal("a build with a failing step reported success")
	}

	// The slow step saw a bare cancellation, which is all a context can carry to
	// the executor - and is exactly why the scheduler has to add the cause.
	if stopped == nil || !errors.Is(stopped, context.Canceled) {
		t.Fatalf("the slow step was not cancelled: %v", stopped)
	}

	// The build blames the real failure, not the step it stopped.
	if !strings.Contains(err.Error(), "Earthfile:9") {
		t.Errorf("the build blames %v, want the step that actually failed", err)
	}

	if strings.Contains(err.Error(), "Earthfile:20") {
		t.Errorf("the build reported the cancelled step beside its cause: %v", err)
	}
}

// A build stopped from outside says which step it stopped, and why.
//
// The case an author meets by pressing Ctrl-C, and the one where a cancellation
// is all there is to report - so it is the cancellation the build hands back.
// Bare `context canceled` there names no step and no reason, which is the
// buildkit behaviour this exists to avoid.
func TestAnExternallyStoppedBuildNamesTheStepAndTheReason(t *testing.T) {
	t.Parallel()

	root := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"img"}}, Meta: ir.Meta{Source: "Earthfile:1"}}
	only := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{"only"}}, Inputs: []*ir.Node{root},
		Meta: ir.Meta{Source: "Earthfile:14"},
	}
	top := &ir.Node{Op: ir.Op{Kind: ir.OpMerge}, Inputs: []*ir.Node{only}, Meta: ir.Meta{Source: "Earthfile:30"}}

	ctx, stop := context.WithCancel(context.Background())

	s := &core.Scheduler{
		Workers: []core.Worker{{ID: "w", IsInvoker: true}},
		Blobs:   allBlobs{},
		Executor: execFunc(func(runCtx context.Context, n *ir.Node) (core.Result, error) {
			if n.Op.Args[0] == "only" {
				stop() // the author changes their mind
				<-runCtx.Done()

				return core.Result{}, runCtx.Err()
			}

			return core.Result{Layer: n.ID(), Captured: true}, nil
		}),
	}

	_, err := s.Run(ctx, &ir.Graph{Root: top})
	if err == nil {
		t.Fatal("a cancelled build reported success")
	}

	// Which step, so the author is not left to guess where it stopped.
	if !strings.Contains(err.Error(), "Earthfile:14") {
		t.Errorf("a stopped build does not name the step it stopped: %v", err)
	}

	// And reachable as a cancellation rather than only as prose.
	if _, ok := errors.AsType[*core.CancelledError](err); !ok {
		t.Errorf("a stopped build is not reported as a cancellation: %v", err)
	}
}
