package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// askedView records the paths a source was told it would be asked about.
type askedView struct {
	fixedView

	told []string
}

func (v *askedView) ViewFor(
	_ context.Context, _ []ir.NodeID, want []string,
) (core.BaseView, error) {
	v.told = want

	return v.fixedView.base, nil
}

// TestASourceIsToldWhichPathsItWillBeAskedAbout.
//
// **A view over a store the host cannot read has to fetch its answers**, and
// fetching them one path at a time is a round trip per file in a prediction.
// The paths are known before the view is made - `tryL2` reads the profile first
// and only then asks for a view - so a source that can batch may be told, and
// one that cannot is asked exactly as before.
//
// An optional interface rather than a changed signature, which is how this
// engine already offers a backend more than the base contract asks for.
func TestASourceIsToldWhichPathsItWillBeAskedAbout(t *testing.T) {
	t.Parallel()

	n := &ir.Node{
		Op:     ir.Op{Kind: ir.OpExec, Args: []string{"cc", "-c", testSource}},
		Inputs: []*ir.Node{{Op: ir.Op{Kind: ir.OpImage, Args: []string{testBase}}}},
	}

	profiles := memProfiles{}
	profiles.Put(core.StepClass(n), core.Observation{
		Reads: map[string]ir.NodeID{"/main.c": digest(1), "/usr/include/stdio.h": digest(2)},
	})

	views := &askedView{fixedView: fixedView{
		base: fakeBase{files: map[string]ir.NodeID{"/main.c": digest(1)}},
	}}

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: &predictionExec{},
		Cache:    newMemCache(),
		Blobs:    allBlobs{},
		Profiles: profiles,
		Views:    views,
		Writer:   testStep,
		Record:   &core.Record{},
	}

	_, err := s.Run(context.Background(), &ir.Graph{Root: n})
	if err != nil {
		t.Fatal(err)
	}

	if len(views.told) != 2 {
		t.Fatalf("the source was told %v, want the two paths the prediction names"+
			"\n  without them a view that has to fetch its answers fetches them"+
			"\n  one round trip at a time", views.told)
	}

	if views.told[0] != "/main.c" || views.told[1] != "/usr/include/stdio.h" {
		t.Errorf("told %v, which is not the prediction sorted", views.told)
	}
}
