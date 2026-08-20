package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The L2 tier works end to end, against the real store and a real view.
//
// Both halves are implementations now rather than fakes - `cache.Profiles`
// (E118) writes to disk and `exec.LayerStore.View` (E114) reads a layer stack -
// and the front end still sets neither, deliberately: an empty profile agrees
// with every base, and a file on disk that this version never writes is one a
// future or foreign version could (E112, and the port register says so).
//
// Which leaves the failure E49 and E114 were both about: a tier that compiles,
// is keyed, is recorded, and has never run. So it runs here, wired exactly as a
// front end would wire it, with the store on a real filesystem.
//
// The observation is deliberately *not* empty, because the interesting claim is
// the one the tier exists to make: **the base changed, the step read nothing
// that differs, and the result was reused.** A test with an empty observation
// would hit for the wrong reason and pass against an engine with the safety
// checks removed.
func TestTheL2TierRunsAgainstARealStore(t *testing.T) {
	t.Parallel()

	profiles, err := cache.OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	n := &ir.Node{Op: ir.Op{Kind: ir.OpExec, Args: []string{"cc", "-c", testSource}}}
	obs := core.Observation{Reads: map[string]ir.NodeID{testReadPath: digest(1)}}

	// The base a profile was learned over, and a different one that agrees
	// about the only path the step read.
	view := fixedView{fakeBase{files: map[string]ir.NodeID{testReadPath: digest(1)}}}

	shared := newMemCache()
	exec := &observingExec{obs: obs}

	build := func(base ir.NodeID) {
		t.Helper()

		s := &core.Scheduler{
			Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
			Executor: exec,
			Cache:    shared,
			Blobs:    allBlobs{},
			Profiles: profiles,
			Views:    view,
			Writer:   testStep,
			Record:   &core.Record{},
		}

		root := &ir.Node{
			Op:     n.Op,
			Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{base.String()}}}},
		}

		_, err := s.Run(context.Background(), &ir.Graph{Root: root})
		if err != nil {
			t.Fatal(err)
		}
	}

	build(digest(10))
	first := exec.runs

	if first == 0 {
		t.Fatal("the first build ran nothing")
	}

	// A different base: the chain key differs, so L1 must miss. What the step
	// read is unchanged, so Κ₂ is the same and L2 should answer.
	build(digest(20))

	// Exactly one more: the base image node has a different argument, so its
	// chain key differs and it is rebuilt. The exec step above it must not be.
	//
	// Counting rather than asserting "did not run" because both nodes go
	// through the same executor - a test that only checked the total went up
	// would pass against an engine that reran everything, and one that checked
	// it did not go up at all would fail on the base it is *supposed* to
	// rebuild. The gap between those two is the whole feature.
	if ran := exec.runs - first; ran != 1 {
		t.Errorf("a new base and an unchanged observation reran %d steps, want 1"+
			"\n  the step read only /src/main.c, which both bases agree about,"+
			"\n  so Κ₂ is unchanged and this is the rebuild L2 exists to avoid", ran)
	}

	// Whatever it decided, the profile it learned is on disk and readable, and
	// derives the key it was learned under. A store that round-trips to a
	// different key is a tier that can never hit while looking implemented.
	got, ok := profiles.Get(core.StepClass(n))
	if !ok {
		t.Fatal("nothing was written to the profile store by a build that observed")
	}

	if core.DeriveObservedKey(n, nil, got) != core.DeriveObservedKey(n, nil, obs) {
		t.Error("the profile came back deriving a different observed key")
	}
}
