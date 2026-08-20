package core_test

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// An isolated WITH DOCKER block is cacheable, and it is the only docker block
// that is.
//
// The scheduler has refused to cache *any* docker step since the construct
// landed, with a comment saying so and calling it "a stopgap with a known ending
// rather than a permanent rule": the daemon outlived the build, so every image an
// earlier build left in it was state the key did not describe.
//
// `--isolate` is that ending (E381). Its daemon starts empty and its storage
// lives in the step's own overlay, which is thrown away with the step, so the
// result *is* a function of the inputs and the key describes it honestly.
//
// The gate narrows on `IsolateDocker` rather than on `NoCache`, deliberately.
// The interpreter already marks every non-isolated block uncacheable, and
// checking that here would be checking the same field twice; checking a
// different one keeps the scheduler's own guarantee - "enforced here rather than
// left to an executor to declare, because an executor that forgot would produce
// exactly the wrong answer silently".
func TestAnIsolatedDockerBlockIsCacheable(t *testing.T) {
	t.Parallel()

	s := newSched(newMemCache(), allBlobs{}, &observingExec{})
	s.Profiles = memProfiles{}
	s.Views = fixedView{fakeBase{}}

	base := &ir.Node{
		Op:       ir.Op{Kind: ir.OpImage, Args: []string{testBaseImage}},
		Platform: amd64,
	}

	n := &ir.Node{
		Op: ir.Op{
			Kind: ir.OpExec, Args: []string{"make"},
			Docker: true, IsolateDocker: true,
		},
		Platform: amd64, Inputs: []*ir.Node{base},
		Meta: ir.Meta{Source: at(11)},
	}

	if _, err := s.Run(context.Background(), &ir.Graph{Root: n}); err != nil {
		t.Fatalf("%v", err)
	}

	if s.Stats.Uncacheable != 0 {
		t.Errorf("an isolated docker block was refused the cache (%d uncacheable"+
			" step(s) at %v); its daemon starts empty and dies with the step, so"+
			" there is nothing about it the key fails to describe",
			s.Stats.Uncacheable, s.Stats.UncacheableAt)
	}
}
