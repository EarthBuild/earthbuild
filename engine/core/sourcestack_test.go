package core_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// deltaExec counts the steps it runs and gives every one a layer of its own,
// except that `fixed` names steps whose delta does not depend on their base -
// a `COPY sub /w/` produces the same bytes whatever was copied before it.
type deltaExec struct {
	mu    sync.Mutex
	ran   map[string]int
	fixed map[string]ir.NodeID
}

func (e *deltaExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ran[n.Meta.Source]++

	layer, ok := e.fixed[n.Meta.Source]
	if !ok {
		layer = n.ID()
	}

	return core.Result{Layer: layer, Captured: true}, nil
}

// An artifact copy keys on everything it copies, not on its source's top layer.
//
// A target's artifact is the whole stack the copy reads (`srcStacks`), but the
// key used to see only `done[src].Layer`. Edit a lower step of the source
// target, have its top step re-produce the delta it made last time - an
// ordinary `COPY --dir a b /w/` where only `a` changed - and the ref did not
// move: the copy was an L1 hit and every step above it served the previous
// build's bytes. Found by rth on midnight-node, where a fixed `res/build.rs`
// never reached the build; `earth` on the same tree rebuilt.
func TestAnArtifactCopyMissesWhenALowerLayerOfItsSourceChanges(t *testing.T) {
	t.Parallel()

	top := ir.DigestOf([]byte("COPY sub /w/"))
	cache := newMemCache()
	e := &deltaExec{ran: map[string]int{}, fixed: map[string]ir.NodeID{at(3): top}}

	build := func(edit string) {
		t.Helper()

		img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testImage}}, Meta: ir.Meta{Source: at(1)}}
		lower := &ir.Node{
			Op:     ir.Op{Kind: ir.OpFile, Args: []string{"d1", "/w/"}, Content: ir.DigestOf([]byte(edit))},
			Inputs: []*ir.Node{img}, Meta: ir.Meta{Source: at(2)},
		}
		upper := &ir.Node{
			Op:     ir.Op{Kind: ir.OpFile, Args: []string{"sub", "/w/"}},
			Inputs: []*ir.Node{lower}, Meta: ir.Meta{Source: at(3)},
		}
		cp := &ir.Node{
			Op:     ir.Op{Kind: ir.OpFile, Args: []string{"/w", "/w"}},
			Inputs: []*ir.Node{img}, Sources: []*ir.Node{upper}, Meta: ir.Meta{Source: at(4)},
		}
		use := &ir.Node{
			Op:     ir.Op{Kind: ir.OpExec, Args: []string{"cat /w/d1/f"}},
			Inputs: []*ir.Node{cp}, Meta: ir.Meta{Source: at(5)},
		}

		s := &core.Scheduler{
			Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
			Executor: e,
			Cache:    cache,
			Blobs:    allBlobs{},
		}

		_, err := s.Run(context.Background(), &ir.Graph{Root: use})
		if err != nil {
			t.Fatal(err)
		}
	}

	build("m1")
	build("m2")

	// The edited step and the one above it rerun on their own changed bases.
	if got := e.ran[at(2)]; got != 2 {
		t.Errorf("the edited step ran %d times over two builds, want 2", got)
	}

	// The copy reads the edited layer, so it must rerun too, and so must what
	// stands on it.
	for _, line := range []int{4, 5} {
		if got := e.ran[at(line)]; got != 2 {
			t.Errorf("%s ran %d times over two builds, want 2 - it was served the first build's result",
				at(line), got)
		}
	}
}
