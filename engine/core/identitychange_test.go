package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// TestAChangedLayerRuleIsNotTheStepsFault.
//
// **The most valuable diagnostic a build tool can emit, pointed at the wrong
// party.** `Diverge` reports non-determinism when every component of a step's
// key is identical and its output is not, and that is exactly what a build sees
// the first time it runs after the engine changes how a layer is named - E656
// changed it twice in one afternoon, for a symlink's mode and for ownership an
// unprivileged unpack could not grant.
//
// Observed on this repository's own bench build:
//
//	context src.txt  NON-DETERMINISM: nothing in the key changed and the output did
//	  every component of the key is identical; the step is not reproducible
//
// The step was perfectly reproducible. The engine had changed underneath it, and
// nothing in the record said so, because a record carried no account of which
// rule produced it.
//
// A user upgrading sees this about their own build and has no way to tell it
// from the real thing - which is worse than not reporting it, because the real
// finding is the one they would then learn to ignore.
func TestAChangedLayerRuleIsNotTheStepsFault(t *testing.T) {
	t.Parallel()

	step := core.StepRecord{
		Ident: "s1", Node: digest(1), Base: digest(2),
		Op: digest(3), Meta: ir.Meta{Description: "Earthfile:4"},
	}

	// Same key, different output: on its face, non-determinism.
	before := &core.Record{Identity: 1, Steps: []core.StepRecord{withLayer(step, digest(10))}}
	after := &core.Record{Identity: 2, Steps: []core.StepRecord{withLayer(step, digest(11))}}

	d := core.Diverge(before, after)

	if d.Cause == core.CauseNonDeterminism {
		t.Fatalf("a step was called non-deterministic because the engine's own"+
			" rule for naming layers changed:\n  %v", d)
	}

	if d.Cause != core.CauseLayerRule {
		t.Fatalf("the divergence was attributed to %v, want the layer rule", d.Cause)
	}
}

// TestARealNonDeterminismSurvivesTheCheck: the whole point is to keep the
// finding, so two records from *one* engine must still say so.
func TestARealNonDeterminismSurvivesTheCheck(t *testing.T) {
	t.Parallel()

	step := core.StepRecord{
		Ident: "s1", Node: digest(1), Base: digest(2),
		Op: digest(3), Meta: ir.Meta{Description: "Earthfile:4"},
	}

	before := &core.Record{Identity: 2, Steps: []core.StepRecord{withLayer(step, digest(10))}}
	after := &core.Record{Identity: 2, Steps: []core.StepRecord{withLayer(step, digest(11))}}

	if d := core.Diverge(before, after); d.Cause != core.CauseNonDeterminism {
		t.Fatalf("a genuinely non-deterministic step was attributed to %v", d.Cause)
	}
}

// TestARecordThatMakesNoClaimDoesNotContradictOne.
//
// **An unstated identity is not a different one.** Every in-memory record
// carries zero, and a comparison between a saved record and a freshly built one
// is the ordinary case - saying zero disagreed with everything reported a rule
// change on every build, which the round-trip test caught within a minute of the
// field existing.
//
// Stored records from before the field are handled by the format version
// instead: they decode to a version this engine no longer reads, so they are no
// comparison at all rather than a wrong one.
func TestARecordThatMakesNoClaimDoesNotContradictOne(t *testing.T) {
	t.Parallel()

	step := core.StepRecord{Ident: "s1", Node: digest(1), Op: digest(3)}

	silent := &core.Record{Steps: []core.StepRecord{withLayer(step, digest(10))}}
	stated := &core.Record{Identity: core.LayerRule, Steps: []core.StepRecord{withLayer(step, digest(11))}}

	if d := core.Diverge(silent, stated); d.Cause == core.CauseLayerRule {
		t.Error("a record that stated no rule was read as stating a different one")
	}
}

// TestABaseChangeIsStillABaseChange: the new cause must not swallow the
// attributions that were already right. A build whose base moved *and* whose
// engine changed is told about the base, which is the thing it can act on.
func TestABaseChangeIsStillABaseChange(t *testing.T) {
	t.Parallel()

	a := core.StepRecord{Ident: "s1", Node: digest(1), Base: digest(2), Op: digest(3)}
	b := core.StepRecord{Ident: "s1", Node: digest(1), Base: digest(3), Op: digest(3)}

	before := &core.Record{Identity: 1, Steps: []core.StepRecord{withLayer(a, digest(10))}}
	after := &core.Record{Identity: 2, Steps: []core.StepRecord{withLayer(b, digest(11))}}

	if d := core.Diverge(before, after); d.Cause != core.CauseBase {
		t.Errorf("a base change was attributed to %v", d.Cause)
	}
}

func withLayer(s core.StepRecord, l ir.NodeID) core.StepRecord {
	s.Layer = l

	return s
}
