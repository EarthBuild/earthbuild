package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// copyRun runs one COPY step over a base and returns how many steps ran.
func copyRun(t *testing.T, profiles core.Profiles, shared *memCache, e *observingExec,
	base ir.NodeID, view core.ViewSource,
) int {
	t.Helper()

	before := e.runs

	n := &ir.Node{
		Op:     ir.Op{Kind: ir.OpFile, Args: []string{"a.txt", "/w/"}},
		Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{base.String()}}}},
	}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: e,
		Cache:    shared,
		Blobs:    allBlobs{},
		Profiles: profiles,
		Views:    view,
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	return e.runs - before
}

// A COPY over a new base is reused when the destination is unchanged.
//
// This is the win the copy observation source exists for. Bump a base image and
// every `COPY` above it rebuilds today, because the chain key includes the base
// - though a copy of an unchanged file into an unchanged destination cannot
// produce anything different (E119).
//
// Two builds over two bases. The base node differs, so it rebuilds and L1
// misses for the copy; the copy observed only `/w`, both bases agree about it,
// so Κ₂ is unchanged and the copy is reused. One step runs instead of two.
func TestACopyIsReusedOverANewBaseWithTheSameDestination(t *testing.T) {
	t.Parallel()

	profiles, err := cache.OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	obs := core.Observation{Reads: map[string]ir.NodeID{"/w": digest(7)}}
	view := fixedView{fakeBase{files: map[string]ir.NodeID{"/w": digest(7)}}}

	shared := newMemCache()
	e := &observingExec{obs: obs}

	if ran := copyRun(t, profiles, shared, e, digest(10), view); ran == 0 {
		t.Fatal("the first build ran nothing")
	}

	if ran := copyRun(t, profiles, shared, e, digest(20), view); ran != 1 {
		t.Errorf("a new base reran %d steps, want 1 - the base itself"+
			"\n  the copy read only /w, which both bases agree about, so its"+
			"\n  observed key is unchanged and this is the rebuild L2 avoids", ran)
	}
}

// And it is not reused when the destination differs.
//
// **The safety half, and the one that decides whether the tier may be switched
// on at all.** `COPY x /w/` places the file *inside* /w when /w is a directory
// and renames onto it when it is not, so a base where /w differs produces a
// different layer. A hit there is I3 - a false cache hit, the one failure the
// whole design exists to prevent.
//
// The prediction is the same in both builds; what changes is the base it is
// checked against, which is exactly what `Consistent` is for.
func TestACopyIsNotReusedWhenItsDestinationDiffers(t *testing.T) {
	t.Parallel()

	profiles, err := cache.OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	obs := core.Observation{Reads: map[string]ir.NodeID{"/w": digest(7)}}

	shared := newMemCache()
	e := &observingExec{obs: obs}

	same := fixedView{fakeBase{files: map[string]ir.NodeID{"/w": digest(7)}}}
	other := fixedView{fakeBase{files: map[string]ir.NodeID{"/w": digest(9)}}}

	if ran := copyRun(t, profiles, shared, e, digest(10), same); ran == 0 {
		t.Fatal("the first build ran nothing")
	}

	if ran := copyRun(t, profiles, shared, e, digest(20), other); ran != 2 {
		t.Errorf("a base whose /w differs reran %d steps, want 2"+
			"\n  the copy's destination decides where its source lands, so a"+
			"\n  different /w is a different result and reusing it is a false hit", ran)
	}
}
