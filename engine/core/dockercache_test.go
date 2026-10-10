package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step inside a WITH DOCKER block is never cached, for now.
//
// The daemon outlives the build that used it, so what a step in the block
// observes includes every image and container an earlier build left behind -
// and none of that is in the key. `RUN docker images` is the plainest case: it
// prints state no key describes.
//
// I7 is not a preference here. A key that cannot bound what a step observed must
// not become a cache entry, and the failure it prevents is the worst kind this
// engine can produce: a build that passes because a previous build left
// something behind, and fails on a machine that never ran it.
//
// This is a stopgap with a known ending. When `--load` and `--pull` land, what
// enters the daemon is declared in the command and therefore keyable, and a
// per-block data-root makes the rest of the state bounded - at which point the
// block becomes cacheable and this rule narrows rather than disappears.
func TestDockerStepsAreNeverCached(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{"docker images"}, Docker: true},
		Meta: ir.Meta{Source: at(5)},
	}

	cache := newMemCache()

	s := &core.Scheduler{
		Workers: []core.Worker{{ID: testLocal, IsInvoker: true}},
		// An executor that claims the result is captured and confined, which is
		// exactly the case the rule has to survive: it is not the sandbox that
		// is in doubt, it is what the sandbox contains.
		Executor: capturingExec{},
		Cache:    cache,
		Blobs:    allBlobs{},
		Writer:   testStep,
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	if cache.len() != 0 {
		t.Errorf("a WITH DOCKER step published %d cache entries, want 0", cache.len())
	}

	if got := s.Record.Steps[0].Outcome; got != core.OutcomeUncaptured {
		t.Errorf("outcome is %v, want uncaptured", got)
	}
}

// An ordinary step beside it is still cached: the rule is about the daemon, not
// about the build that happens to contain one.
func TestAnOrdinaryStepBesideADockerStepIsStillCached(t *testing.T) {
	t.Parallel()

	plain := &ir.Node{
		Op:   ir.Op{Kind: ir.OpExec, Args: []string{testCommand}},
		Meta: ir.Meta{Source: at(3)},
	}

	cache := newMemCache()

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: testLocal, IsInvoker: true}},
		Executor: capturingExec{},
		Cache:    cache,
		Blobs:    allBlobs{},
		Writer:   testStep,
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: plain})
	if err != nil {
		t.Fatal(err)
	}

	if cache.len() == 0 {
		t.Error("an ordinary step was not cached")
	}
}
