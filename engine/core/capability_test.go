package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/sim"
)

func m1Caps() *core.Capabilities {
	return &core.Capabilities{
		Milestone: "M1 (FROM, RUN, SAVE ARTIFACT)",
		Ops:       map[ir.OpKind]bool{ir.OpImage: true, ir.OpExec: true},
	}
}

// TestUnsupportedConstructsAreRefused is invariant I10. An engine that cannot
// evaluate something must say so, not approximate.
func TestUnsupportedConstructsAreRefused(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{Root: &ir.Node{
		Op:   ir.Op{Kind: ir.OpHost, Args: []string{testCommand}},
		Meta: ir.Meta{Source: testSite},
	}}

	s := newSched(newMemCache(), allBlobs{}, &sim.Executor{Seed: 1})
	s.Capabilities = m1Caps()

	_, err := s.Run(context.Background(), g)
	if err == nil {
		t.Fatal("an unsupported construct was accepted")
	}

	var ue *core.UnsupportedError
	if !errors.As(err, &ue) {
		t.Fatalf("error is not an UnsupportedError: %v", err)
	}

	// The message has to answer the user's next three questions without them
	// having to ask: what, where, and what now.
	msg := err.Error()
	for _, want := range []string{"LOCALLY", testSite, "M9", "--engine=buildkit"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, msg)
		}
	}
}

// TestRefusalHappensBeforeAnythingRuns is the property that makes a partial
// engine safe to ship.
//
// Refusing after three steps have run leaves a tree that is neither the old
// result nor the new one, and a user with no way to tell which parts are real.
func TestRefusalHappensBeforeAnythingRuns(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}}
	a := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"a"}}, Inputs: []*ir.Node{img}}
	b := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"b"}}, Inputs: []*ir.Node{a}}

	// The unsupported construct is last, so a naive engine would run two steps
	// before noticing.
	root := &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{"deploy"}}, Inputs: []*ir.Node{b}}

	exec := &sim.Executor{Seed: 1}

	s := newSched(newMemCache(), allBlobs{}, exec)
	s.Capabilities = m1Caps()

	_, err := s.Run(context.Background(), &ir.Graph{Root: root})
	if err == nil {
		t.Fatal("expected a refusal")
	}

	if len(exec.Log) != 0 {
		t.Errorf("%d steps ran before the refusal; nothing should have", len(exec.Log))
	}
}

// TestSupportedGraphsAreUnaffected: the gate must not cost anything when it has
// nothing to refuse.
func TestSupportedGraphsAreUnaffected(t *testing.T) {
	t.Parallel()

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}}
	g := &ir.Graph{Root: &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{testCommand}}, Inputs: []*ir.Node{img},
	}}

	s := newSched(newMemCache(), allBlobs{}, &sim.Executor{Seed: 1})
	s.Capabilities = m1Caps()

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatalf("a graph within capabilities was refused: %v", err)
	}
}

// TestNoCapabilitiesMeansNoRestriction keeps the simulator and the tests free
// of ceremony: an engine that declares nothing restricts nothing.
func TestNoCapabilitiesMeansNoRestriction(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{Root: &ir.Node{Op: ir.Op{Kind: ir.OpHost, Args: []string{testCommand}}}}

	s := newSched(newMemCache(), allBlobs{}, &sim.Executor{Seed: 1})
	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatalf("an unrestricted engine refused something: %v", err)
	}
}
