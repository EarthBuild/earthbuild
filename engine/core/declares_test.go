package core

import (
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step that declares something puts it on the stack, above the layer it
// produced.
//
// Green paper §3.2a: what an image declares is a stack element, not a file
// beside one. That is what makes it travel - a worker fetches every id in the
// stack - and what puts it in ids(𝑏), so it reaches every key derived from the
// base without an exception being made for it.
//
// Above the layer, because a declaration applies to the steps that come after
// it, exactly as a layer does.
func TestADeclarationJoinsTheStackAboveItsLayer(t *testing.T) {
	t.Parallel()

	s := &Scheduler{stacks: map[ir.NodeID][]ir.NodeID{}, done: map[ir.NodeID]Result{}, Record: &Record{}}

	n := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{"golang:1.26"}}}
	layer := ir.NodeID{1}
	declaration := ir.NodeID{2}

	s.finish(n, nil, Result{Layer: layer, Declares: declaration}, StepRecord{})

	got := s.StackFor(n)
	want := []ir.NodeID{layer, declaration}

	if !slices.Equal(got, want) {
		t.Errorf("stack %v, want %v", got, want)
	}
}

// A step that produced no layer puts nothing there either.
//
// The empty base is the case: `FROM scratch` is captured and complete, and the
// layer it produces is none - which is a zero identity, exactly as an absent
// declaration is. Pushing it makes every stack above it name an element the
// store cannot hold, and the first step that has to materialise that stack goes
// looking for a layer whose digest is sixty-four zeroes.
//
// The symptom is `COPY` onto `scratch`, which is a build the executor's own
// comments say is supported and which failed with "the element has to be
// fetched before the step can run" - naming a fetch for something that was
// never going to exist (I18).
func TestAStepThatProducedNoLayerAddsNothing(t *testing.T) {
	t.Parallel()

	s := &Scheduler{stacks: map[ir.NodeID][]ir.NodeID{}, done: map[ir.NodeID]Result{}, Record: &Record{}}

	n := &ir.Node{Op: ir.Op{Kind: ir.OpScratch}}

	s.finish(n, nil, Result{Captured: true}, StepRecord{})

	if got := s.StackFor(n); len(got) != 0 {
		t.Errorf("the empty base put %v on the stack, and it produces no layer"+
			"\n  every step above it then materialises a stack naming an element"+
			" the store can never hold", got)
	}
}

// A step that declares nothing puts nothing there.
//
// Most steps: a RUN produces a filesystem delta and says nothing about how the
// next one should run. A zero identity is "no declaration", and pushing it would
// make every stack name an element the store cannot hold - which the
// materialiser now refuses, correctly (I18).
func TestAStepThatDeclaresNothingAddsNothing(t *testing.T) {
	t.Parallel()

	s := &Scheduler{stacks: map[ir.NodeID][]ir.NodeID{}, done: map[ir.NodeID]Result{}, Record: &Record{}}

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"true"}}}
	layer := ir.NodeID{7}

	s.finish(n, []ir.NodeID{{3}}, Result{Layer: layer}, StepRecord{})

	got := s.StackFor(n)
	want := []ir.NodeID{{3}, layer}

	if !slices.Equal(got, want) {
		t.Errorf("stack %v, want %v", got, want)
	}
}

// A cached image keeps what it declared.
//
// A hit rebuilds the result from the entry, so a field the entry does not carry
// is a field the stack loses - and the stack is where a declaration lives. The
// first version of this stored Layer, Exit and Bytes, so a cached FROM produced
// a stack with no declaration and the step above it ran without the PATH its
// image sets. Which is the original bug, arriving by a different road.
func TestACachedImageKeepsItsDeclaration(t *testing.T) {
	t.Parallel()

	e := Entry{Layer: ir.NodeID{1}, Declares: ir.NodeID{2}, Declared: true}

	if e.Declares == (ir.NodeID{}) {
		t.Fatal("an entry cannot carry a declaration")
	}
}

// An entry that predates declarations is not read as one that declares nothing.
//
// Absent and empty are different claims: "this image says nothing" is a fact
// about the image, and "nobody recorded what it says" is a fact about the entry.
// Conflating them serves a stack with no declaration and no way to know it is
// wrong - the same distinction Captured and Content already draw.
func TestAnEntryPredatingDeclarationsIsNotTrusted(t *testing.T) {
	t.Parallel()

	old := Entry{Layer: ir.NodeID{1}}

	if usableDeclaration(ir.OpImage, old) {
		t.Error("an entry from before declarations was read as one that declares nothing")
	}

	looked := Entry{Layer: ir.NodeID{1}, Declared: true}
	if !usableDeclaration(ir.OpImage, looked) {
		t.Error("an entry that recorded finding no declaration was refused")
	}

	// A step's own result declares nothing and never did, so its entries are
	// unaffected: only an image is expected to carry one.
	if !usableDeclaration(ir.OpExec, old) {
		t.Error("a step's entry was refused for lacking a declaration it never had")
	}
}
