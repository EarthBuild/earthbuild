package cli

import (
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A build carrying a construct that must always run records nothing skippable.
//
// **Both keys, not just Κ_job.** `planHolds` compares a plan fingerprint and
// nothing else, so gating only the record's reads left key A serving the skip -
// and key A is the one a build that watched nothing falls back to, which is
// every build containing `LOCALLY`, because a host step is never watched.
//
// The gate belongs here rather than at the ask. `askAutoSkip` runs *before*
// planning, which is the whole point of the flag, so there is no plan to
// consult at the moment the question is asked. A build that never records a
// skippable answer can never be skipped, whenever it is asked.
func TestAMustRunBuildRecordsNothingSkippable(t *testing.T) {
	t.Parallel()

	const (
		target = "+build"
		plat   = "linux/arm64"
		plan   = "fingerprint-of-the-plan"
	)

	shape := ir.NodeID{1, 2, 3}
	store := skipRecordStore{at: filepath.Join(t.TempDir(), "records")}

	// A clean build of the same target recorded a skippable answer.
	keep(store, target, plat, shape, plan, []hostInput{
		{Path: "a.txt", Kind: inputFile, Digest: "d"},
	})

	if was, ok := store.get(target, plat); !ok || !was.planHolds(plan) {
		t.Fatal("the clean build recorded nothing, so this tests nothing")
	}

	// Then LOCALLY was added, and the build ran again.
	keepUnskippable(store, target, plat, "Earthfile:12 runs LOCALLY")

	rec, ok := store.get(target, plat)
	if !ok {
		t.Fatal("the record vanished: a build that must run still has a target")
	}

	if rec.planHolds(plan) {
		t.Error("key A still skips: the plan fingerprint is compared without" +
			"\n  regard to whether the plan contains something that must run")
	}

	if rec.stillHolds(shape, t.TempDir()) {
		t.Error("key C still skips")
	}

	if rec.MustRun == "" {
		t.Error("nothing says why, so the operator sees a flag that stopped working")
	}
}

// And it is asked of a real Earthfile, not a hand-built graph.
//
// The two halves of this work fail independently: `mustRun` can be right about
// a graph nobody builds that way, and `noteBuild` can be wired to something
// that never sees a host step. The interesting case is the one in the middle -
// `LOCALLY` written in a file, planned by the interpreter, reaching the gate.
func TestMustRunSeesLocallyInAnEarthfile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, source string
		want         bool
	}{
		{"a host step", "main:\n    LOCALLY\n    RUN echo hi\n", true},
		{"a --no-cache step", "main:\n    FROM alpine:3.22\n    RUN --no-cache echo hi\n", true},
		{"neither", "main:\n    FROM alpine:3.22\n    RUN echo hi\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := interp.Build("VERSION 0.8\n"+tc.source, testMainTarget,
				interp.WithContext(t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}

			if got := mustRun(p) != ""; got != tc.want {
				t.Errorf("mustRun said %v, want %v - reason %q", got, tc.want, mustRun(p))
			}
		})
	}
}
