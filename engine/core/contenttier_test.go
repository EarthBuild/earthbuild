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

// oneContent says every stack it is told about materialises the same tree.
type oneContent struct {
	by map[ir.NodeID]ir.NodeID // keyed on the stack's first element, which is the base here
}

func (oneContent) Has(ir.NodeID) bool { return true }

func (o oneContent) TreeOf(stack []ir.NodeID) (ir.NodeID, bool) {
	if len(stack) == 0 {
		return ir.NodeID{}, false
	}

	c, ok := o.by[stack[0]]

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

// restampingCapture is a step that produces a new layer every run and the same
// bytes, and tells the store what it made - as a real capture does by writing a
// manifest beside the layer.
type restampingCapture struct {
	seed  int
	store notingContent
	runs  int
}

func (e *restampingCapture) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	e.runs++

	// A different layer each run, the clock being in the identity (I8).
	h := ir.NewHasher()
	id := n.ID()
	h.Fixed(id[:])
	h.Count(e.seed)
	layer := h.Sum()

	// The same bytes each run.
	c := ir.NewHasher()
	c.Str("content")
	c.Fixed(id[:])
	content := c.Sum()

	e.store.note(layer, content)

	// **A stack, with an element the store has nothing to say about.** A base
	// holds declarations as well as trees (§3.2a) and only a tree has a
	// manifest to fold - so a step over an image has an element with no content
	// in its base, which is the case that made Κₜ underivable in practice while
	// every test that modelled a base as one captured layer passed.
	decl := ir.NewHasher()
	decl.Str("declaration")
	decl.Fixed(id[:])

	return core.Result{
		Layer: layer, Layers: []ir.NodeID{decl.Sum(), layer},
		Content: content, Captured: true,
	}, nil
}

// notingContent folds a stack from what it was told each layer holds, as a
// store folds the manifests written beside them - and knows nothing about a
// layer nobody captured, which is the declaration case.
type notingContent struct{ by map[ir.NodeID]ir.NodeID }

func (notingContent) Has(ir.NodeID) bool { return true }

func (n notingContent) note(layer, content ir.NodeID) { n.by[layer] = content }

func (n notingContent) TreeOf(stack []ir.NodeID) (ir.NodeID, bool) {
	h := ir.NewHasher()

	var known int

	for _, id := range stack {
		// An element nobody captured contributes nothing to the tree, exactly
		// as a declaration does: it is a stack element and not a layer.
		if c, ok := n.by[id]; ok {
			known++
			h.Fixed(c[:])
		}
	}

	if known == 0 {
		return ir.NodeID{}, false
	}

	return h.Sum(), true
}

// A --no-cache step does not invalidate the step above it.
//
// **What `--no-cache` means, and what it came to mean.** It says "always run
// this step". Because Κ₁ names a base by its layer ids and a rerun stamps new
// mtimes on what it writes (I8), it also meant "and rebuild everything above
// it" - so a `RUN --no-cache git rev-parse HEAD > /version` on an unchanged
// commit rebuilt the whole tail for bytes that had not moved.
//
// Κₜ (4.5a) names the base by what it holds, so the step above hits whenever the
// uncacheable step below produced the same bytes. One that genuinely differs -
// `date > /stamp` - still invalidates, which is the point.
//
// **Written because the unit tests passed while a real build did nothing.** The
// fake store above answers only about layers something captured, as a real one
// answers only where a manifest was written; the first version answered about
// everything, and so never met the element that made Κₜ underivable in practice.
func TestANoCacheStepDoesNotInvalidateTheStepAboveIt(t *testing.T) {
	t.Parallel()

	store := notingContent{by: map[ir.NodeID]ir.NodeID{}}
	cache := newMemCache()

	graph := func() *ir.Graph {
		always := &ir.Node{
			Op:       ir.Op{Kind: ir.OpExec, Args: []string{"stamp"}, NoCache: true},
			Platform: amd64,
		}

		return &ir.Graph{Root: &ir.Node{
			Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc", testSource}}, Platform: amd64,
			Inputs: []*ir.Node{always},
		}}
	}

	run := func(seed int) *core.Scheduler {
		s := newSched(cache, store, &restampingCapture{seed: seed, store: store})
		s.Record = &core.Record{}

		if _, err := s.Run(context.Background(), graph()); err != nil {
			t.Fatal(err)
		}

		return s
	}

	if first := run(1); first.Stats.Hits != 0 {
		t.Fatal("the first build hit something, so it was not cold")
	}

	second := run(2)

	if second.Stats.ContentHits == 0 {
		t.Errorf("the step above a --no-cache step rebuilt, though the bytes"+
			" beneath it had not moved"+
			"\n  hits=%d contentHits=%d l2=%d", second.Stats.Hits,
			second.Stats.ContentHits, second.Stats.L2Hits)
	}
}
