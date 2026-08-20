package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// blindExec is an observation source that saw nothing and does not know it.
//
// Not a straw man: it is the shape of every source that is wired up before it
// works. `Observations()` returns an empty `core.Observation{}` in the overlay
// materialiser, the guest and the host executor today, each with a comment
// saying S5 will fill it in. The day one of them is filled in, the way it is
// switched on is `Observed: true` - and a source that is attached too late, or
// misses the exec itself, reports exactly this.
type blindExec struct{}

func (blindExec) Run(_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID) (core.Result, error) {
	return core.Result{
		Layer:    n.ID(),
		Captured: true,
		Observed: true, // claims completeness
		Observation: core.Observation{
			Reads:    map[string]ir.NodeID{},
			Listings: map[string]ir.NodeID{},
			// Incomplete is false: this source does not know it missed anything.
		},
	}, nil
}

// A step that ran a program and observed nothing did not observe.
//
// `Consistent(obs, base)` iterates the reads, the negatives and the listings.
// On an empty observation all three loops are empty, so it returns **true for
// every base in existence** - and Κ₂ then says "this result is valid wherever
// this step is run". `RUN gcc -c main.c` would hit against a base with a
// different compiler.
//
// E109's companion invariant, one layer up: an exec step reads its own
// executable before it can read anything else. `/bin/true` is a file in the
// base image. So a *complete* observation of a step that ran a program cannot
// be empty, and one that is empty is a source reporting silence as fact - which
// is precisely what `Incomplete` exists to prevent and precisely what a source
// that has not been implemented yet will not set.
//
// **The trap this closes.** `Observed` and `Incomplete` are two booleans whose
// correct setting is a matter of the source author remembering. Four findings
// this session were a rule established in one place and not applied at its
// sibling; this is the same shape aimed forwards, at a sibling that does not
// exist yet. The scheduler now decides rather than trusting, so the future
// source cannot get it wrong by omission - only by lying, which is a different
// and much louder mistake.
func TestAnEmptyObservationOfAnExecStepIsNotKeyed(t *testing.T) {
	t.Parallel()

	// Over a base, because that is what makes an empty observation a lie: a
	// step standing on nothing and reporting nothing is being honest (E125).
	n := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{testTrueWord}},
		Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBase}}}},
	}

	cache := newMemCache()

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: blindExec{},
		Cache:    cache,
		Blobs:    allBlobs{},
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	// Two Κ₁ entries now: the base image step and the exec above it.
	if cache.len() != 2 {
		t.Errorf("cache holds %d entries, want the two Κ₁ entries", cache.len())
	}

	if got := s.Record.Steps[1].ObservedKey; got != (core.Key{}) {
		t.Error("a step that ran a program and reported reading nothing" +
			" produced an observed-input key, which every base satisfies")
	}
}

// A step that runs no program may legitimately observe nothing.
//
// The rule is about exec steps specifically, and stating it that way rather than
// as "no empty observations" matters: an `OpImage` step reads nothing from a
// base because it *has* no base, and refusing its observation would be the
// mirror mistake - a rule with fewer cases than the world.
func TestAnEmptyObservationOfANonExecStepIsFine(t *testing.T) {
	t.Parallel()

	n := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: blindExec{},
		Cache:    newMemCache(),
		Blobs:    allBlobs{},
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	if got := s.Record.Steps[0].ObservedKey; got == (core.Key{}) {
		t.Error("a step that genuinely reads nothing was refused an observed key")
	}
}
