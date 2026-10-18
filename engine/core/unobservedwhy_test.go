package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// reasonedExec gives each step whatever the caller mapped its first argument
// to, so a build can contain one step that nothing watched and one whose
// watcher failed and said why.
type reasonedExec struct {
	by map[string]core.Result
}

func (e *reasonedExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	res := e.by[n.Op.Args[0]]
	res.Layer, res.Captured = n.ID(), true

	return res, nil
}

// A stated reason outranks the default one.
//
// `noteUnobserved` keeps the first reason it is given, which is right when the
// reasons are equally informative and wrong when they are not: "nothing
// observed this step" is what a step of an unwatchable kind always says, and
// there are many of those, so it reaches the field first and hides the one
// reason that names a defect. A cold substrate build reported
// `Earthfile:197: nothing observed this step` for a `COPY`, while the `RUN
// cargo build` above it - the step whose observation the build actually needed
// - had its reason discarded (E620's shape, found again downstream of it).
//
// Where must move with why. A reason attached to another step's source line
// sends the reader to a step that is not the one that failed.
func TestAStatedReasonOutranksTheDefaultOne(t *testing.T) {
	t.Parallel()

	const (
		unwatched = "unwatched"
		reasoned  = "reasoned"
		why       = "the step's observation never arrived: frame too large"
	)

	exec := &reasonedExec{by: map[string]core.Result{
		// Nothing was watching: the generic reason, and it happens first.
		unwatched: {},
		// Something was watching, it lost the answer, and it said so.
		reasoned: {Observation: core.Observation{Incomplete: true, Why: []string{why}}},
	}}

	img := &ir.Node{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}
	low := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{unwatched}}, Platform: amd64,
		Inputs: []*ir.Node{img}, Meta: ir.Meta{Source: "Earthfile:1"},
	}
	high := &ir.Node{
		Op: ir.Op{Kind: ir.OpExec, Args: []string{reasoned}}, Platform: amd64,
		Inputs: []*ir.Node{low}, Meta: ir.Meta{Source: "Earthfile:2"},
	}

	s := newSched(newMemCache(), allBlobs{}, exec)
	s.Record, s.Profiles = &core.Record{}, memProfiles{}

	if _, err := s.Run(context.Background(), &ir.Graph{Root: high}); err != nil {
		t.Fatal(err)
	}

	if s.Stats.Unobserved != 2 {
		t.Fatalf("%d unobserved steps, want 2 - the build is not the one this tests",
			s.Stats.Unobserved)
	}

	if !strings.Contains(s.Stats.UnobservedWhy, "never arrived") {
		t.Errorf("reported %q, want the stated reason %q"+
			"\n  the generic reason arrived first and kept the field, so the one"+
			"\n  reason naming a defect was discarded", s.Stats.UnobservedWhy, why)
	}

	if s.Stats.UnobservedWhere != "Earthfile:2" {
		t.Errorf("reported at %q, want Earthfile:2 - where must move with why,"+
			"\n  or the reader is sent to a step that did not fail",
			s.Stats.UnobservedWhere)
	}
}
