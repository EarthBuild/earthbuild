package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// **A build where everything hit cache still has something true to say.**
//
// It is the case every developer meets first: `--auto-skip` turned on against a
// store they already have. No step runs, so nothing watched anything, so there
// are no reads to record - and the record was therefore never written and the
// flag never skipped anything, ever.
//
// What such a build *did* establish is that every chain key hit, which covers
// the declared inputs. So it records those: the plan fingerprint, which is
// coarser than the reads and is not nothing. The first build that actually runs
// upgrades the record, and until then the flag works on the machine people have.
func TestAFullyCachedBuildRecordsThePlanFingerprint(t *testing.T) {
	t.Parallel()

	got := recordFor("build", "linux/arm64", aShape('a'), "a-plan-fingerprint", nil)

	if got.Plan != "a-plan-fingerprint" {
		t.Errorf("a fully cached build recorded plan %q", got.Plan)
	}

	if got.Shape != "" || len(got.Inputs) != 0 {
		t.Errorf("a fully cached build claimed reads it never saw: %+v", got)
	}

	if !got.planHolds("a-plan-fingerprint") {
		t.Error("the record it wrote does not answer for the build that wrote it")
	}
}

// A build that ran records what it read, and the fingerprint besides.
func TestABuildThatRanRecordsBoth(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})

	ran := &core.Record{Steps: []core.StepRecord{
		ranAndWatched(placedAt(), read("/w/src/a.txt")),
	}}

	in, err := hostInputsOfBuild(ran, profilesOf{}, map[string]bool{contextLayer: true}, root)
	if err != nil {
		t.Fatal(err)
	}

	got := recordFor("build", "linux/arm64", aShape('a'), "a-plan-fingerprint", in)

	if got.Shape == "" || len(got.Inputs) != 1 || got.Key == "" {
		t.Errorf("a build that ran recorded %+v", got)
	}

	if got.Plan != "a-plan-fingerprint" {
		t.Error("a build that ran did not also record the fingerprint")
	}
}

// **And a cached build must not erase what a real one learned.** Downgrading a
// record from what the build read to what it declared would undo the mechanism
// every time somebody ran a build that happened to hit cache.
func TestACachedBuildDoesNotDowngradeAnExistingRecord(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/a.txt": "one"})
	at := filepath.Join(t.TempDir(), "records")
	s := skipRecordStore{at: at}

	full := recorded(t, root, aShape('a'), "/w/src/a.txt")
	full.Plan = "first"
	s.put(full)

	// A later build, fully cached, with a fingerprint of its own: it gathered
	// no reads, so it has none to offer.
	keep(s, "build", "linux/arm64", aShape('a'), "second", nil)

	back, ok := s.get("build", "linux/arm64")
	if !ok {
		t.Fatal("the record vanished")
	}

	if len(back.Inputs) != len(full.Inputs) || back.Key != full.Key {
		t.Errorf("a cached build downgraded the record to %+v", back)
	}

	if back.Plan != "second" {
		t.Errorf("the fingerprint was not brought up to date: %q", back.Plan)
	}
}

// **The coarse record has to be consulted, not merely written.**
//
// A fully cached build records the plan fingerprint and nothing else. If the
// asking side only ever compares the reads, that record is written by every
// build and read by none - which is the whole bootstrap doing nothing, and is
// what happened: `planHolds` existed, was tested on its own, and was never
// called.
func TestACoarseRecordIsUsedWhenThereAreNoReads(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src.txt": "one"})
	s := skipRecordStore{at: filepath.Join(t.TempDir(), "records")}

	in := shapeInput{Source: []byte(shapeSrc), Target: "build", Platform: "linux/arm64"}

	shape, err := shapeOf(in)
	if err != nil {
		t.Fatal(err)
	}

	fingerprint := "a-plan-fingerprint"
	s.put(recordFor("build", "linux/arm64", shape, fingerprint, nil))

	skip, _, _, err := wouldSkipPlan(in, root, s, fingerprint)
	if err != nil || !skip {
		t.Errorf("an unchanged plan against a coarse record: skip=%t err=%v", skip, err)
	}

	// And a changed one is not skipped.
	skip, _, _, err = wouldSkipPlan(in, root, s, "another-fingerprint")
	if err != nil || skip {
		t.Errorf("a changed plan against a coarse record: skip=%t err=%v", skip, err)
	}
}

// A full record is preferred over the coarse one: the reads are the finer
// answer and a file nobody read must not rebuild.
func TestAFullRecordWinsOverTheFingerprint(t *testing.T) {
	t.Parallel()

	root := tree(t, map[string]string{"src/read.txt": "one", "src/README.md": "one"})
	s := skipRecordStore{at: filepath.Join(t.TempDir(), "records")}

	in := shapeInput{Source: []byte(shapeSrc), Target: "build", Platform: "linux/arm64"}

	shape, err := shapeOf(in)
	if err != nil {
		t.Fatal(err)
	}

	inputs, err := hostInputsFrom(map[string]bool{contextLayer: true},
		placedAt(), read("/w/src/read.txt"), root)
	if err != nil {
		t.Fatal(err)
	}

	s.put(recordFor("build", "linux/arm64", shape, "a-plan-fingerprint", inputs))

	// The plan fingerprint moves - a file in the context changed - and the
	// reads do not, because nothing read that file.
	err = os.WriteFile(filepath.Join(root, "src/README.md"), []byte("two"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	skip, _, _, err := wouldSkipPlan(in, root, s, "a-moved-fingerprint")
	if err != nil || !skip {
		t.Errorf("a file nobody read moved the fingerprint and the reads were not"+
			" consulted: skip=%t err=%v", skip, err)
	}
}
