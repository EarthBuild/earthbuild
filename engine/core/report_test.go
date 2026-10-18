package core_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

func obs(files map[string]byte) core.Observation {
	o := core.Observation{Reads: map[string]ir.NodeID{}, Listings: map[string]ir.NodeID{}}
	for p, b := range files {
		o.Reads[p] = digest(b)
	}

	return o
}

func rec(ident string, o core.Observation, layer byte) core.StepRecord {
	return core.StepRecord{
		Ident: ident, Observation: o, Observed: true, Layer: digest(layer),
		Op: digest(1), Env: digest(2), Plat: digest(3), Base: digest(4),
		Meta: ir.Meta{Description: "+build", Source: "./Earthfile:41"},
	}
}

// TestReportNamesTheFiles: a report that stops at the step sends the reader off
// to diff by hand, which is the job the tool exists to do.
func TestReportNamesTheFiles(t *testing.T) {
	t.Parallel()

	a := rec("s", obs(map[string]byte{testRustSource: 1, testLockPath: 1}), 10)
	b := rec("s", obs(map[string]byte{testRustSource: 2, testLockPath: 1}), 11)

	out := core.Report(core.Diverge(
		&core.Record{Steps: []core.StepRecord{a}},
		&core.Record{Steps: []core.StepRecord{b}},
	))

	if !strings.Contains(out, testRustSource) {
		t.Errorf("report does not name the changed file:\n%s", out)
	}

	if strings.Contains(out, testLockPath) {
		t.Errorf("report named an unchanged file:\n%s", out)
	}

	if !strings.Contains(out, "./Earthfile:41") {
		t.Errorf("report does not say where the step is:\n%s", out)
	}
}

// TestSuspiciousPathsArePromoted is B.5's ranking rule, and the line that
// actually saves someone an afternoon.
//
// A step that depends on .git/HEAD is almost always a bug, and it must appear
// above the source file the reader expected to see - not buried under it.
func TestSuspiciousPathsArePromoted(t *testing.T) {
	t.Parallel()

	before := obs(map[string]byte{
		"src/a.rs": 1, "src/b.rs": 1, "src/c.rs": 1, "src/d.rs": 1,
		"src/e.rs": 1, "src/f.rs": 1, testGitHead: 1,
	})
	after := obs(map[string]byte{
		"src/a.rs": 2, "src/b.rs": 2, "src/c.rs": 2, "src/d.rs": 2,
		"src/e.rs": 2, "src/f.rs": 2, testGitHead: 2,
	})

	out := core.Report(core.Diverge(
		&core.Record{Steps: []core.StepRecord{rec("s", before, 10)}},
		&core.Record{Steps: []core.StepRecord{rec("s", after, 11)}},
	))

	if !strings.Contains(out, testGitHead) {
		t.Errorf("the suspicious path was not shown at all:\n%s", out)
	}

	if !strings.Contains(out, "git state") {
		t.Errorf("the suspicious path was shown without saying why:\n%s", out)
	}

	// Seven changed, five shown: the summary must account for the rest.
	if !strings.Contains(out, "and 2 more") {
		t.Errorf("report did not summarise the remainder:\n%s", out)
	}
}

// TestReportIsReproducible: a diagnostic that reorders itself between runs is
// one nobody can diff or paste into an issue.
func TestReportIsReproducible(t *testing.T) {
	t.Parallel()

	before := obs(map[string]byte{"a": 1, "b": 1, "c": 1, "d": 1})
	after := obs(map[string]byte{"a": 2, "b": 2, "c": 2, "d": 2})

	first := core.Report(core.Diverge(
		&core.Record{Steps: []core.StepRecord{rec("s", before, 10)}},
		&core.Record{Steps: []core.StepRecord{rec("s", after, 11)}},
	))

	for range 20 {
		again := core.Report(core.Diverge(
			&core.Record{Steps: []core.StepRecord{rec("s", before, 10)}},
			&core.Record{Steps: []core.StepRecord{rec("s", after, 11)}},
		))
		if again != first {
			t.Fatalf("report varies between runs:\n%s\n---\n%s", first, again)
		}
	}
}

// TestNonDeterminismReportSaysSo: the most valuable diagnostic must be phrased
// so the reader cannot mistake it for an ordinary cache miss.
func TestNonDeterminismReportSaysSo(t *testing.T) {
	t.Parallel()

	same := obs(map[string]byte{testSourcePath: 1})

	out := core.Report(core.Diverge(
		&core.Record{Steps: []core.StepRecord{rec("s", same, 10)}},
		&core.Record{Steps: []core.StepRecord{rec("s", same, 11)}},
	))

	if !strings.Contains(out, "NON-DETERMINISM") {
		t.Errorf("non-determinism not named:\n%s", out)
	}

	if !strings.Contains(out, "not reproducible") {
		t.Errorf("non-determinism reported without explaining it:\n%s", out)
	}
}

// TestCounterfactualQuantifiesL2 is the measurement that says what
// observed-input caching is worth, computed from records alone and *before*
// the feature is switched on.
//
// If it comes back small across a corpus, that is an argument against building
// it - which is the point of measuring rather than assuming.
func TestCounterfactualQuantifiesL2(t *testing.T) {
	t.Parallel()

	read := obs(map[string]byte{testSourcePath: 1})

	// Both builds read the same file with the same contents, yet the step ran
	// again and produced a different layer: the base moved underneath a step
	// that could not observe it.
	prev := &core.Record{Steps: []core.StepRecord{rec("s", read, 10)}}

	cur := rec("s", read, 11)
	cur.Outcome = core.OutcomeMiss

	would, missed := core.Counterfactual(prev, &core.Record{Steps: []core.StepRecord{cur}})

	if missed != 1 {
		t.Fatalf("missed = %d, want 1", missed)
	}

	if would != 1 {
		t.Errorf("wouldHaveHit = %d, want 1: nothing the step read changed", would)
	}
}

// TestCounterfactualIgnoresRealChanges: a step whose inputs genuinely changed
// would not have hit either, and counting it would overstate the case.
func TestCounterfactualIgnoresRealChanges(t *testing.T) {
	t.Parallel()

	prev := &core.Record{Steps: []core.StepRecord{
		rec("s", obs(map[string]byte{testSourcePath: 1}), 10),
	}}

	cur := rec("s", obs(map[string]byte{testSourcePath: 99}), 11)
	cur.Outcome = core.OutcomeMiss

	would, _ := core.Counterfactual(prev, &core.Record{Steps: []core.StepRecord{cur}})
	if would != 0 {
		t.Errorf("wouldHaveHit = %d, want 0: the file the step reads changed", would)
	}
}
