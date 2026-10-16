package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cache"
	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// Κ₂ does not cross a change of environment, and must not.
//
// **This test exists to refute an optimisation, not to describe a feature.**
//
// A build argument is an environment variable inside the step (E580), and
// `StepClass` hashes the environment - so `ARG SALT` beside a command that never
// mentions SALT gives every value of SALT a profile class of its own, and the
// observation recorded under one is invisible to a build using another. Measured
// on the case the tier exists for, that costs the whole of it: 21s and a miss
// with the argument declared, 1s and an L2 hit without it (E612). Real
// Earthfiles are parameterised by `ARG` nearly everywhere.
//
// The obvious fix - a second, environment-free profile class, consulted when the
// exact one misses - was written, and this test refuted it. It can never add a
// hit. Finding a prediction is only the first half; the entry is then looked up
// under `DeriveObservedKey`, which hashes the full environment (green paper 4.6,
// 𝒮(ε)). So a prediction borrowed across two environments derives a key no entry
// was ever stored under, and if the key did match, the exact class would have
// hit already. The fallback is pure cost, provably.
//
// And (4.6) is right to include it. **The environment is an input no observation
// can capture**: the tracer sees the paths a step opens, and a `getenv` is a read
// of memory the process was handed at exec. A tier that crossed it would be
// guessing that the step ignored a value it was given, which is the false-hit
// shape I3 exists to prevent.
//
// So the cost is real, the mechanism is right, and the way out is not here: it
// is an Earthfile that does not declare arguments a step never reads.
func TestTheObservedTierDoesNotCrossAnEnvironmentChange(t *testing.T) {
	t.Parallel()

	profiles, err := cache.OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	obs := core.Observation{Reads: map[string]ir.NodeID{testReadPath: digest(1)}}
	view := fixedView{fakeBase{files: map[string]ir.NodeID{testReadPath: digest(1)}}}

	shared := newMemCache()
	exec := &observingExec{obs: obs}

	build := func(base ir.NodeID, salt string) {
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
			Op: ir.Op{
				Kind: ir.OpExec,
				Args: []string{"cc", "-c", testSource},
				Env:  map[string]string{"SALT": salt},
			},
			Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{base.String()}}}},
		}

		_, err := s.Run(context.Background(), &ir.Graph{Root: root})
		if err != nil {
			t.Fatal(err)
		}
	}

	build(digest(10), "one")
	first := exec.runs

	if first == 0 {
		t.Fatal("the first build ran nothing")
	}

	build(digest(20), "two")

	// Two: the base, whose argument differs, and the step, which cannot be
	// reused across an environment it may have read. The companion test
	// TestTheL2TierRunsAgainstARealStore is the same build with the environment
	// held still, and reruns one - the pair is what pins the boundary.
	if ran := exec.runs - first; ran != 2 {
		t.Errorf("a step was reused across a changed environment: %d reran, want 2"+
			"\n  the environment is an input no observation captures, so reusing"+
			"\n  a result across it is a guess that the step ignored what it was"+
			"\n  handed - the false hit I3 exists to prevent", ran)
	}
}
