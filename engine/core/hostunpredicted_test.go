package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step that is never published is not a step with a missing prediction.
//
// `CACHE /root/.m2` makes a step uncacheable, deliberately: what it produces may
// depend on what was in the mount, and no key bounds that (I3). Such a step is
// never looked up and never published - so nothing is ever recorded for its
// class, and it was being counted as *unpredicted* on every build for ever.
//
// That is the third time the same mistake has been made. A step with no base
// cannot have a prediction about one (E218); a `FROM` cannot either (E223); and
// a step the engine refuses to cache cannot have one by construction. Each time
// the count fired on something that could never have qualified, and each time it
// made the number say "the tier is broken" when the answer was "not applicable".
//
// It also stops the pointless lookup: `tryL2` was consulted for these steps and
// its answer discarded by `hit && !host`, which is a store read and a view
// computation per step for a result that could not be used (E226).
func TestAnUncacheableStepIsNotCountedAsUnpredicted(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		op   ir.Op
	}{
		{"a cache mount", ir.Op{Kind: ir.OpExec, Args: []string{"make"}, NoCache: true}},
		{"a host step", ir.Op{Kind: ir.OpHost, Args: []string{"make"}}},
		{"a WITH DOCKER step", ir.Op{Kind: ir.OpExec, Args: []string{"make"}, Docker: true}},
	} {
		s := newSched(newMemCache(), allBlobs{}, &observingExec{})
		s.Profiles = memProfiles{}
		s.Views = fixedView{fakeBase{}}

		base := &ir.Node{
			Op:       ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}},
			Platform: amd64,
		}

		op := tc.op
		n := &ir.Node{Op: op, Platform: amd64, Inputs: []*ir.Node{base}}

		_, err := s.Run(context.Background(), &ir.Graph{Root: n})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}

		if s.Stats.L2Unpredicted != 0 {
			t.Errorf("%s: counted %d unpredicted (%v); this step can never have"+
				" a prediction, so the count says the tier is broken when the"+
				" answer is that it does not apply",
				tc.name, s.Stats.L2Unpredicted, s.Stats.L2UnpredictedAt)
		}
	}
}
