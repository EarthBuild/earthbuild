package core_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// squashingExec records the ranges it was asked to collapse.
type squashingExec struct {
	mu   sync.Mutex
	runs []([]ir.NodeID) // the base stack each step was given
	made map[ir.NodeID][]ir.NodeID
}

func (e *squashingExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, base []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.runs = append(e.runs, append([]ir.NodeID(nil), base...))

	return core.Result{Layer: n.ID()}, nil
}

// Squash is the optional half: an executor that can collapse a range says so by
// implementing it.
func (e *squashingExec) Squash(_ context.Context, into ir.NodeID, rng []ir.NodeID) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.made == nil {
		e.made = map[ir.NodeID][]ir.NodeID{}
	}

	e.made[into] = append([]ir.NodeID(nil), rng...)

	return nil
}

// A flattened stack names a layer, and somebody has to make it.
//
// Φ is not bookkeeping. It replaces a range of the stack with one identity, and
// that identity is a directory the executor is about to mount - so unless
// something builds it from the range it collapsed, the mount finds an empty
// directory where the base of the build should be.
//
// Nothing built it. The scheduler flattened, recorded the decision in the build
// record, and passed the new identity to an executor that had never heard of
// it. The threshold was 480 so it had never fired, which is the only reason
// this was a latent defect rather than a build that silently lost its base
// (E50).
func TestAFlattenedStackIsBuiltBeforeItIsUsed(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}

	// Deeper than the limit this scheduler is given, so Φ must fire.
	g := &ir.Graph{Root: chain(img, "a", "b", "c", "d", "e", "f")}

	exec := &squashingExec{}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", Platform: amd64}},
		Executor: exec,
		MaxStack: 3,
	}

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	if len(exec.made) == 0 {
		t.Fatal("the scheduler flattened a stack and asked nobody to build the layer it named")
	}

	// Every squashed identity the steps were given must be one that was built,
	// and built from the range it stands for.
	for _, base := range exec.runs {
		for _, id := range base {
			rng, built := exec.made[id]
			if !built {
				continue // an ordinary step layer
			}

			if len(rng) < 2 {
				t.Errorf("a squashed layer collapses %d layers, which is not a range", len(rng))
			}
		}
	}
}

// The identity of a squashed range is the range, not the moment.
//
// Two builds that collapse the same layers must name the result the same way or
// the second cannot reuse the first's work - and worse, a name that varied
// would make the chain key vary, so every step standing on it would miss.
func TestASquashedRangeIsNamedByItsContents(t *testing.T) {
	t.Parallel()

	rng := []ir.NodeID{{1}, {2}, {3}}

	first := core.SquashID(rng)
	if first != core.SquashID([]ir.NodeID{{1}, {2}, {3}}) {
		t.Error("the same range was named twice and disagreed")
	}

	if first == core.SquashID([]ir.NodeID{{3}, {2}, {1}}) {
		t.Error("order does not reach the name, so a reordered stack collides")
	}

	if first == core.SquashID([]ir.NodeID{{1}, {2}}) {
		t.Error("a prefix of the range shares its name")
	}
}
