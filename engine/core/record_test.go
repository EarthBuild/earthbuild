package core_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// buildRec runs a graph and returns its record.
func buildRec(t *testing.T, g *ir.Graph, exec core.Executor) *core.Record {
	t.Helper()

	s := newSched(newMemCache(), allBlobs{}, exec)
	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	return s.Record
}

// execAt builds a step with a *positional* identity, as a real Earthfile would:
// two builds of the same line correlate even when the command on it changed.
func execAt(src string, args ...string) func(*ir.Node) *ir.Node {
	return func(base *ir.Node) *ir.Node {
		return &ir.Node{
			Op: ir.Op{Kind: ir.OpExec, Args: args}, Platform: amd64,
			Inputs: []*ir.Node{base},
			Meta:   ir.Meta{Description: args[0], Source: src},
		}
	}
}

func exec1(args ...string) func(*ir.Node) *ir.Node { return execAt(at(2), args...) }

var alpine = &ir.Node{
	Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64,
	Meta: ir.Meta{Description: "FROM alpine:3.22", Source: at(1)},
}

// nodeExec derives each layer from the node, so records reflect the graph
// rather than the order things happened to run.
type nodeExec struct{ salt byte }

func (e nodeExec) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	l := n.ID()
	l[31] ^= e.salt // salt lets a test force a differing result

	return core.Result{Layer: l, Captured: true}, nil
}

// TestIdenticalBuildsDoNotDiverge is the baseline: the tool must not cry wolf.
func TestIdenticalBuildsDoNotDiverge(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{Root: exec1(testCommand)(alpine)}

	a := buildRec(t, g, nodeExec{})
	b := buildRec(t, g, nodeExec{})

	if d := core.Diverge(a, b); d.Cause != core.CauseNone {
		t.Errorf("identical builds diverged: %s", d)
	}
}

// TestNonDeterminismIsNamed is the diagnostic no chain-keyed system can emit.
//
// Every component of the key is identical - same base, same command, same
// environment, same platform - and the outputs differ anyway. That is not a
// cache miss to be explained away; it is the finding, and the tool has to say
// so in those words.
func TestNonDeterminismIsNamed(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{Root: exec1("date > /out")(alpine)}

	a := buildRec(t, g, nodeExec{salt: 0})
	b := buildRec(t, g, nodeExec{salt: 0xff}) // same inputs, different output

	d := core.Diverge(a, b)
	if d.Cause != core.CauseNonDeterminism {
		t.Fatalf("cause = %v, want CauseNonDeterminism", d.Cause)
	}

	if d.A.ChainKey != d.B.ChainKey {
		t.Error("classified as non-determinism but the chain keys differ")
	}
}

// TestDivergenceIsAttributed: locating the step is not enough. The report has
// to say which part changed, or the user is left diffing by hand.
func TestDivergenceIsAttributed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		b    *ir.Graph
		want core.Cause
	}{
		{"command changed", &ir.Graph{Root: exec1(testCommand, "-j8")(alpine)}, core.CauseOp},

		// A changed base image reports at the *image* step, whose op - the
		// reference - is what changed. The exec step below it also differs, but
		// the earliest divergence is the cause and the rest is collateral.
		{"base image changed", &ir.Graph{Root: exec1(testCommand)(&ir.Node{
			Op: ir.Op{Kind: ir.OpImage, Args: []string{"alpine:3.23"}}, Platform: amd64,
			Meta: ir.Meta{Source: at(1)},
		})}, core.CauseOp},

		{"environment changed", &ir.Graph{Root: &ir.Node{
			Op:       ir.Op{Kind: ir.OpExec, Args: []string{testCommand}, Env: map[string]string{"CC": "clang"}},
			Platform: amd64, Inputs: []*ir.Node{alpine},
			Meta: ir.Meta{Source: at(2)},
		}}, core.CauseEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := buildRec(t, &ir.Graph{Root: exec1(testCommand)(alpine)}, nodeExec{})
			b := buildRec(t, tc.b, nodeExec{})

			if d := core.Diverge(a, b); d.Cause != tc.want {
				t.Errorf("cause = %v, want %v", d.Cause, tc.want)
			}
		})
	}
}

// TestDivergenceIsTheEarliest: a report naming a late step when an earlier one
// also differs sends the reader to the wrong place. Everything downstream of a
// divergence differs too, and only the first one is a cause.
func TestDivergenceIsTheEarliest(t *testing.T) {
	t.Parallel()

	chainOf := func(base *ir.Node, cmds ...string) *ir.Node {
		cur := base
		for i, c := range cmds {
			cur = execAt(fmt.Sprintf("Earthfile:%d", i+2), c)(cur)
		}

		return cur
	}

	a := buildRec(t, &ir.Graph{Root: chainOf(alpine, "one", "two", "three")}, nodeExec{})
	b := buildRec(t, &ir.Graph{Root: chainOf(alpine, "one", "CHANGED", "three")}, nodeExec{})

	d := core.Diverge(a, b)

	// "one" is shared and identical, so it must not be blamed; the divergence
	// is at "two", which exists only in A.
	if d.Meta.Description == "one" {
		t.Error("blamed a step that was identical in both builds")
	}

	if d.Cause == core.CauseNone {
		t.Fatal("no divergence found between differing builds")
	}
}

// TestRecordsCarryOutcomes: a record that does not say whether a step hit or
// ran cannot answer "why did this rebuild", which is the question users
// actually ask.
func TestRecordsCarryOutcomes(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{Root: exec1(testCommand)(alpine)}
	cache := newMemCache()

	first := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w1", Platform: amd64, IsInvoker: true}},
		Executor: nodeExec{}, Cache: cache, Blobs: allBlobs{}, Writer: "t",
	}
	_, err := first.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	for _, st := range first.Record.Steps {
		if st.Outcome != core.OutcomeMiss {
			t.Errorf("first build: %s recorded as %s, want miss", st.Meta.Description, st.Outcome)
		}
	}

	second := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w1", Platform: amd64, IsInvoker: true}},
		Executor: nodeExec{}, Cache: cache, Blobs: allBlobs{}, Writer: "t",
	}
	_, err = second.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	for _, st := range second.Record.Steps {
		if st.Outcome != core.OutcomeL1Hit {
			t.Errorf("rebuild: %s recorded as %s, want L1 hit", st.Meta.Description, st.Outcome)
		}
	}
}

// TestRecordsAreDigestsNotContent: records must stay small enough to keep for
// the last N builds, which means structure and digests, never bytes.
func TestRecordsAreDigestsNotContent(t *testing.T) {
	t.Parallel()

	g := &ir.Graph{Root: exec1(testCommand)(alpine)}

	rec := buildRec(t, g, nodeExec{})
	if len(rec.Steps) != 2 {
		t.Fatalf("recorded %d steps, want 2", len(rec.Steps))
	}

	for _, st := range rec.Steps {
		if st.Class == (core.Key{}) {
			t.Error("step recorded without its class; L2 lookups cannot be explained")
		}

		if st.ChainKey == (core.Key{}) {
			t.Error("step recorded without its chain key")
		}
	}
}

// TestCauseBaseIsForDownstreamSteps exercises the classifier directly.
//
// CauseBase describes a step whose *inputs* differ while its own command,
// environment and platform are unchanged. In a whole build it is rarely the
// earliest divergence - something upstream caused the inputs to differ, and
// that is reported instead - so it is tested here on records rather than
// through a build.
func TestCauseBaseIsForDownstreamSteps(t *testing.T) {
	t.Parallel()

	same := func(b byte) core.StepRecord {
		return core.StepRecord{
			Ident: at(9),
			Op:    digest(1), Env: digest(2), Plat: digest(3),
			Base:  digest(b),
			Layer: digest(100 + b),
		}
	}

	a := &core.Record{Steps: []core.StepRecord{same(1)}}
	b := &core.Record{Steps: []core.StepRecord{same(2)}}

	if d := core.Diverge(a, b); d.Cause != core.CauseBase {
		t.Errorf("cause = %v, want CauseBase", d.Cause)
	}
}
