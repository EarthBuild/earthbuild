package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An empty profile does not authorise an L2 hit.
//
// The publish side now refuses to key an exec step that observed nothing. This
// is the other half, and it is not redundant: a profile is a *prediction*, and
// where it comes from is a trust question rather than an arithmetic one.
//
// `tryL2` asks the profile store what this class of step usually reads, checks
// `Consistent(pred, view)`, and looks up `DeriveObservedKey(n, nil, pred)`. On an
// empty prediction `Consistent` is trivially true against every base, so the
// whole check reduces to "is there an entry under the empty-observation key" -
// and at S6 the answer comes from a fleet this engine did not write (A5).
//
// Two independent halves, because a check that only holds while the other half
// holds is one refactor from being nothing at all.
func TestAnEmptyProfileDoesNotAuthoriseAHit(t *testing.T) {
	t.Parallel()

	// **Over a base.** The first version of this test used a bare node with no
	// inputs, so the step stood on nothing - and a step that stands on nothing
	// and reports reading nothing is being honest, which the rule now says
	// (E125). The situation this test describes is a step that *had* a base and
	// a prediction naming none of it, and the fixture has to say so.
	n := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"cc", "-c", testSource}},
		Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBase}}}},
	}

	cache := newMemCache()

	// A cache entry sitting under the key an empty observation derives - which
	// is what a peer publishing a badly-instrumented run would leave behind.
	cache.Put(core.DeriveObservedKey(n, nil, core.Observation{}), core.Entry{
		Layer: digest(99), Writer: "somebody-else",
	})

	profiles := memProfiles{}
	profiles.Put(core.StepClass(n), core.Observation{}) // predicts nothing

	exec := &observingExec{obs: core.Observation{Reads: map[string]ir.NodeID{"/main.c": digest(1)}}}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: exec,
		Cache:    cache,
		Blobs:    allBlobs{},
		Profiles: profiles,
		Views:    fixedView{fakeBase{files: map[string]ir.NodeID{"/main.c": digest(1)}}},
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	if exec.runs == 0 {
		t.Error("the step did not run: an empty prediction was treated as" +
			" agreeing with the base, and a stranger's entry was served for it")
	}
}
