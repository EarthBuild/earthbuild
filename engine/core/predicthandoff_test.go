package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// predictionExec keeps what the scheduler handed it, so a test can ask what the
// executor was told rather than what the scheduler meant.
type predictionExec struct {
	predicted []string
	runs      int
}

func (e *predictionExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.runs++
	e.predicted = n.Meta.ReadsPredicted

	return core.Result{
		Layer:       digest(7),
		Observation: core.Observation{Reads: map[string]ir.NodeID{"/main.c": digest(1)}},
	}, nil
}

// TestAStepThatRunsIsToldWhatItsClassReadLastTime.
//
// **The engine already knows and was not saying.** `tryL2` asks the profile
// store what this class of step usually reads, to decide whether an observed-key
// entry can be trusted. When that lookup misses - the entry is not there, the
// base has moved - the step runs, and it runs against a base assembled whole,
// because `ReadsPredicted` was left empty and `wouldPrime` needs it.
//
// So lazy materialisation was reachable only on a fleet worker, which is told
// its prediction in the assignment's hints. Everywhere else the comment on
// `ReadsPredicted` said it plainly: "a worker fills it from the assignment's
// hints; everywhere else it is empty".
//
// It costs nothing to say: the prediction was fetched anyway, one lookup
// earlier, for a different question.
func TestAStepThatRunsIsToldWhatItsClassReadLastTime(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"cc", "-c", testSource}},
		Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBase}}}},
	}

	profiles := memProfiles{}
	profiles.Put(core.StepClass(n), core.Observation{
		Reads: map[string]ir.NodeID{"/main.c": digest(1), "/usr/include/stdio.h": digest(2)},
	})

	exec := &predictionExec{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: exec,
		Cache:    newMemCache(),
		Blobs:    allBlobs{},
		Profiles: profiles,
		Views:    fixedView{fakeBase{files: map[string]ir.NodeID{"/main.c": digest(1)}}},
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	if exec.runs == 0 {
		t.Fatal("the step did not run, so the handoff was never exercised")
	}

	if len(exec.predicted) != 2 {
		t.Fatalf("the executor was told %v, want the two paths the class read"+
			"\n  without them `wouldPrime` is false and the base is assembled"+
			"\n  whole, however little of it the step opens", exec.predicted)
	}

	// Sorted, because a prediction reaches a fragment request and a request that
	// varied with map order would fetch the same paths under different names.
	if exec.predicted[0] != "/main.c" || exec.predicted[1] != "/usr/include/stdio.h" {
		t.Errorf("the prediction came out as %v, which is not sorted", exec.predicted)
	}
}

// TestAStepWithNoProfileIsToldNothing: an empty prediction has to stay empty.
// `Prime` reads "nothing predicted" as "materialise nothing", and a base that
// looked primed but held nothing would leave a step faulting on every path it
// opened.
func TestAStepWithNoProfileIsToldNothing(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"cc", "-c", testSource}},
		Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBase}}}},
	}

	exec := &predictionExec{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: exec,
		Cache:    newMemCache(),
		Blobs:    allBlobs{},
		Profiles: memProfiles{},
		Views:    fixedView{fakeBase{files: map[string]ir.NodeID{"/main.c": digest(1)}}},
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	if len(exec.predicted) != 0 {
		t.Errorf("a step whose class nobody has seen was told %v", exec.predicted)
	}
}
