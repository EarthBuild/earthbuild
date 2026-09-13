package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// layeringExec gives every node a layer derived from its identity, so two
// different bases really are two different layers.
type layeringExec struct{ runs int }

func (e *layeringExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.runs++

	return core.Result{Layer: n.ID(), Captured: true}, nil
}

// oneContent says every layer it is told about holds the same bytes.
type oneContent struct {
	by map[ir.NodeID]ir.NodeID
}

func (oneContent) Has(ir.NodeID) bool { return true }

func (o oneContent) ContentOf(id ir.NodeID) (ir.NodeID, bool) {
	c, ok := o.by[id]

	return c, ok
}

// A base rebuilt into a different layer, holding the same bytes, is a hit.
//
// **The eviction case.** A layer's identity hashes its mtimes (I8), so a
// deterministic step rebuilt - after a prune, on a fresh machine, anywhere it is
// not pulled - produces a different id, and Κ₁ misses for every step above it
// although nothing those steps can observe has changed. Measured on two cold
// builds of examples/rust-layered into separate stores: fourteen of the eighteen
// results that carry a delta agreed about their content and disagreed about
// their id.
//
// The two bases here are two nodes so that the base layer genuinely differs; in
// the case this models they are one node built twice, which a shared action
// cache would otherwise serve from the first build and prove nothing.
func TestARebuiltBaseWithTheSameContentIsAHit(t *testing.T) {
	t.Parallel()

	image := func(ref string) *ir.Node {
		return &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{ref}}, Platform: amd64}
	}
	over := func(base *ir.Node) *ir.Graph {
		return &ir.Graph{Root: &ir.Node{
			Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc", testSource}}, Platform: amd64,
			Inputs: []*ir.Node{base},
		}}
	}

	// What both bases hold, which is the same thing.
	//
	// Known *before* either build, as a real store knows it: a manifest is
	// written beside a layer at capture, so by the time a step above it
	// publishes, its base's content can be asked.
	held := digest(77)
	first, second := image(testBaseImage), image("alpine:3.23")
	blobs := oneContent{by: map[ir.NodeID]ir.NodeID{
		first.ID():  held,
		second.ID(): held,
	}}

	cache := newMemCache()

	run := func(g *ir.Graph) *core.Scheduler {
		s := newSched(cache, blobs, &layeringExec{})
		s.Record = &core.Record{}

		if _, err := s.Run(context.Background(), g); err != nil {
			t.Fatal(err)
		}

		return s
	}

	if cold := run(over(first)); cold.Stats.Hits != 0 {
		t.Fatal("the first build hit something, so it was not cold")
	}

	rebuilt := run(over(second))

	if rebuilt.Stats.Hits != 0 {
		t.Fatal("the chain key hit, so the base did not differ and this proves nothing")
	}

	if rebuilt.Stats.ContentHits == 0 {
		t.Errorf("a different base holding the same bytes produced no content-key hit"+
			"\n  hits=%d contentHits=%d l2=%d", rebuilt.Stats.Hits,
			rebuilt.Stats.ContentHits, rebuilt.Stats.L2Hits)
	}
}
