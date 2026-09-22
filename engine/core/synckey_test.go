package core_test

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A `COPY --sync` is never reused over another base, however alike.
//
// **Its result depends on the base's clock, which Κ₂ does not see.** A file
// the copy writes must come out newer than everything already in the base -
// cargo's `target/` above all - or the compiler calls the edit fresh and ships
// the build from before it. A delta recorded over one base carries the time it
// was written there; replayed over a base whose artefacts are newer, the edited
// source reads as older than what was built from its predecessor. A wrong
// build, not a slow one.
//
// The mirror of TestACopyIsReusedOverANewBaseWithTheSameDestination: the same
// two bases, the same observation, and the one thing that differs is `--sync`.
func TestASyncCopyIsNeverReusedOverAnotherBase(t *testing.T) {
	t.Parallel()

	profiles, err := cache.OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	obs := core.Observation{Reads: map[string]ir.NodeID{"/w": digest(7)}}
	view := fixedView{fakeBase{files: map[string]ir.NodeID{"/w": digest(7)}}}

	shared := newMemCache()
	e := &observingExec{obs: obs}

	if ran := copyRunWith(t, profiles, shared, e, digest(10), view, true); ran == 0 {
		t.Fatal("the first build ran nothing")
	}

	if ran := copyRunWith(t, profiles, shared, e, digest(20), view, true); ran != 2 {
		t.Errorf("a new base reran %d steps, want 2 - the base and the copy"+
			"\n  a --sync copy's writes must be newer than the base they land on,"+
			"\n  so a delta recorded over another base is a false hit", ran)
	}
}

// Nor over a rebuilt base holding the same bytes: Κₜ is refused too.
//
// Κₜ takes the clock out of the base on purpose, which is the one thing a
// `--sync` result depends on. Two bases with one tree and different mtimes
// under `target/` are the case, and it is the ordinary one: every cold rebuild.
func TestASyncCopyDerivesNoContentKey(t *testing.T) {
	t.Parallel()

	stack := []ir.NodeID{digest(1)}
	blobs := knownTrees{stack[0].String(): digest(77)}

	n := &ir.Node{
		Op: ir.Op{
			Kind: ir.OpFile, Args: []string{"src", "/w/"}, Sync: true, DirCopy: true,
		},
		Inputs:   []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}}, Platform: amd64}},
		Platform: amd64,
	}

	if _, ok := core.DeriveContentKey(n, stack, nil, blobs); ok {
		t.Error("a --sync copy derived a content key, which names its base without the clock" +
			"\n  its writes must be newer than that base, so only Κ₁ may serve it")
	}

	// And the same copy without --sync still derives one, or this proves nothing.
	n.Op.Sync = false
	if _, ok := core.DeriveContentKey(n, stack, nil, blobs); !ok {
		t.Error("a plain copy derived no content key; the control is broken")
	}
}
