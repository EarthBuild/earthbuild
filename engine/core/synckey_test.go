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

// syncCopyNode is the node copyRunWith builds, so a test can derive its keys.
func syncCopyNode(base ir.NodeID) *ir.Node {
	return &ir.Node{
		Op:     ir.Op{Kind: ir.OpFile, Args: []string{"a.txt", "/w/"}, Sync: true, DirCopy: true},
		Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{base.String()}}}},
	}
}

// The lookup refuses a Κ₂ entry for a `--sync` copy even when one exists.
//
// **Each half on its own.** The publish side never writes this entry, so a test
// that only runs builds cannot tell whether the lookup guard is there - and an
// entry does exist wherever an engine before the fix wrote one, or a peer that
// has not got it serves one. Planted here as either would have left it.
func TestASyncCopyIgnoresAnObservedEntryAlreadyInTheCache(t *testing.T) {
	t.Parallel()

	profiles, err := cache.OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	obs := core.Observation{Reads: map[string]ir.NodeID{"/w": digest(7)}}
	view := fixedView{fakeBase{files: map[string]ir.NodeID{"/w": digest(7)}}}

	n := syncCopyNode(digest(10))
	profiles.Put(core.StepClass(n), obs)

	shared := newMemCache()
	shared.Put(core.DeriveObservedKey(n, nil, obs), core.Entry{Layer: digest(99)})

	if ran := copyRunWith(t, profiles, shared, &observingExec{obs: obs}, digest(20), view, true); ran != 2 {
		t.Errorf("ran %d steps, want 2 - the copy was served from a planted Κ₂ entry"+
			"\n  a --sync copy's writes must be newer than the base they land on,"+
			"\n  which no observed key can say", ran)
	}
}

// And a `--sync` copy publishes nothing to Κ₂: no profile, no observed key.
//
// The other half, tested without the lookup guard's help.
func TestASyncCopyPublishesNoObservedKey(t *testing.T) {
	t.Parallel()

	profiles, err := cache.OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	obs := core.Observation{Reads: map[string]ir.NodeID{"/w": digest(7)}}
	view := fixedView{fakeBase{files: map[string]ir.NodeID{"/w": digest(7)}}}

	shared := newMemCache()
	if ran := copyRunWith(t, profiles, shared, &observingExec{obs: obs}, digest(10), view, true); ran == 0 {
		t.Fatal("the build ran nothing")
	}

	n := syncCopyNode(digest(10))

	if _, ok := profiles.Get(core.StepClass(n)); ok {
		t.Error("a --sync copy recorded a profile, so a later build can predict it into Κ₂")
	}

	if _, ok := shared.Get(core.DeriveObservedKey(n, nil, obs)); ok {
		t.Error("a --sync copy published a Κ₂ entry, which names its base without the clock")
	}
}
