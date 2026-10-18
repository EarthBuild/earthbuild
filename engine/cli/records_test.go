package cli

import (
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// stepFor builds a record entry with distinguishable component digests.
func stepFor(ident string, base, op, env, plat, layer byte) core.StepRecord {
	return core.StepRecord{
		Ident: ident,
		Node:  ir.NodeID{layer},
		Base:  ir.NodeID{base},
		Op:    ir.NodeID{op},
		Env:   ir.NodeID{env},
		Plat:  ir.NodeID{plat},
		Layer: ir.NodeID{layer},
		Meta:  ir.Meta{Source: "Earthfile:5", Description: "RUN make"},
	}
}

// A build's record outlives the build, or `Diverge` has nothing to compare.
//
// `Diverge` is green paper B.4 - the walk that answers "why did this rebuild",
// "is this step deterministic", "why does it work locally and not in CI" from
// two records rather than from two builds. The plan's stage table lists it under
// S0 as **real**.
//
// It has no non-test caller, and it cannot have one: a record is assembled in
// memory, three of its nineteen fields are printed, and it is dropped when the
// process exits. There has never been a second record to compare the first
// against.
//
// So the record is written to the store beside the layers it describes. Only
// what attribution reads - the component digests, the identity, the location -
// because B.5's file-level detail needs an Observation that S5 does not yet
// produce, and serialising an unbounded map of paths to support a report that
// cannot run would be storage spent on nothing.
func TestARecordSurvivesTheBuildThatMadeIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	want := &core.Record{Steps: []core.StepRecord{
		stepFor("+probe/0", 0x10, 0x20, 0x30, 0x40, 0x50),
		stepFor("+probe/1", 0x11, 0x21, 0x31, 0x41, 0x51),
	}}

	err := saveRecord(dir, testProbe, want)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := loadRecord(dir, testProbe)
	if !ok {
		t.Fatal("the record did not come back")
	}

	if len(got.Steps) != len(want.Steps) {
		t.Fatalf("%d steps went in and %d came out", len(want.Steps), len(got.Steps))
	}

	for i, w := range want.Steps {
		g := got.Steps[i]
		if g.Ident != w.Ident || g.Base != w.Base || g.Op != w.Op ||
			g.Env != w.Env || g.Plat != w.Plat || g.Layer != w.Layer {
			t.Errorf("step %d did not survive:\n  want %+v\n  got  %+v", i, w, g)
		}

		// Meta is what the report names the step by. A round trip that kept the
		// digests and lost the location would attribute a divergence to a
		// twelve-character hash.
		if g.Meta.Source != w.Meta.Source || g.Meta.Description != w.Meta.Description {
			t.Errorf("step %d lost its location: %+v", i, g.Meta)
		}
	}
}

// A missing record is a first build, not a failure.
//
// Every build is somebody's first, and one that refused to run because it had
// no history would be a build tool that cannot be installed.
func TestNoPreviousRecordIsNotAnError(t *testing.T) {
	t.Parallel()

	if _, ok := loadRecord(t.TempDir(), testProbe); ok {
		t.Error("a record appeared where none was written")
	}
}

// An unreadable record is a first build too.
//
// The same rule the action cache follows: a damaged claim is a miss, never an
// error, because handing a corrupted file the power to stop a build that would
// otherwise succeed is the wrong trade. Here it is stronger still - this record
// is a diagnostic, and a diagnostic that can fail a build is worse than no
// diagnostic.
func TestADamagedRecordIsIgnored(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := writeFile(filepath.Join(dir, "records", "probe.json"), "{not json")
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := loadRecord(dir, testProbe); ok {
		t.Error("a damaged record was offered as a comparison")
	}
}

// The round trip preserves what attribution depends on.
//
// The point of the persistence, and the half a field-by-field comparison misses:
// two records that survive intact must still produce the *same finding*. A
// serialisation that dropped `Op` would round-trip every other field and
// silently reclassify every command change as non-determinism - a diagnosis
// that sends the reader looking for a flaky step that does not exist.
func TestAttributionSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, tc := range []struct {
		name string
		b    core.StepRecord
		want core.Cause
	}{
		{"the command changed", stepFor("+probe/0", 0x10, 0x99, 0x30, 0x40, 0x59), core.CauseOp},
		{"the base changed", stepFor("+probe/0", 0x99, 0x20, 0x30, 0x40, 0x59), core.CauseBase},
		{"an environment value changed", stepFor("+probe/0", 0x10, 0x20, 0x99, 0x40, 0x59), core.CauseEnv},
		{"the platform changed", stepFor("+probe/0", 0x10, 0x20, 0x30, 0x99, 0x59), core.CausePlatform},
		{
			"nothing in the key changed",
			stepFor("+probe/0", 0x10, 0x20, 0x30, 0x40, 0x59),
			core.CauseNonDeterminism,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := &core.Record{Steps: []core.StepRecord{stepFor("+probe/0", 0x10, 0x20, 0x30, 0x40, 0x50)}}

			err := saveRecord(dir, tc.name, a)
			if err != nil {
				t.Fatal(err)
			}

			back, ok := loadRecord(dir, tc.name)
			if !ok {
				t.Fatal("the record did not come back")
			}

			b := &core.Record{Steps: []core.StepRecord{tc.b}}

			if got := core.Diverge(back, b).Cause; got != tc.want {
				t.Errorf("after a round trip the cause is %v, not %v", got, tc.want)
			}
		})
	}
}

// A build calls this, not merely records.go.
//
// The first version of this guard asked whether anything called `core.Diverge`,
// and passed the moment `whyItReran` was written - because that *is* a non-test
// caller. **The seam had moved up one level and the guard followed it.**
//
// Which is the failure mode of a source-level check: it proves a call exists
// somewhere, and "somewhere" grows a new floor every time a helper is
// extracted. So it asks about the outermost function instead, the one whose
// only possible caller is a build, and names the file it must not be satisfied
// by.
func TestABuildAsksWhyItReran(t *testing.T) {
	t.Parallel()

	callers, err := nonTestFilesContaining(".", "whyItReran(")
	if err != nil {
		t.Fatal(err)
	}

	delete(callers, "records.go")

	if len(callers) == 0 {
		t.Error("nothing outside records.go calls whyItReran" +
			"\n  the record is written every build and compared by nobody," +
			"\n  so \"why did this rebuild\" has an implementation and no answer")
	}
}

// And the record is saved, or there is never a previous one to compare against.
//
// The other half of the same seam, and the half that fails silently: reading a
// record that is never written produces no error, no output and no clue - just
// a diagnostic that is permanently quiet.
func TestABuildSavesItsRecord(t *testing.T) {
	t.Parallel()

	callers, err := nonTestFilesContaining(".", "saveRecord(")
	if err != nil {
		t.Fatal(err)
	}

	delete(callers, "records.go")

	if len(callers) == 0 {
		t.Error("nothing outside records.go calls saveRecord")
	}
}
