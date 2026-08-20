package exec

import (
	"context"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The empty base produces a result the cache may keep.
//
// `Captured` is what tells the scheduler a result is complete enough to file: an
// uncaptured one is a step that ran and produced nothing the engine can name, so
// it is never cached and runs again next time (green paper I11).
//
// A `FROM scratch` produces the empty layer, which is complete - and a build
// that starts from it would otherwise re-run its base on every build, for ever,
// with nothing in the output to say why (E468).
func TestTheEmptyBaseIsCacheable(t *testing.T) {
	t.Parallel()

	var e Executor

	got, err := e.Run(context.Background(),
		&ir.Node{Op: ir.Op{Kind: ir.OpScratch}}, core.Worker{}, nil, nil)
	if err != nil {
		t.Fatalf("the empty base failed: %v", err)
	}

	if !got.Captured {
		t.Error("the empty base is not captured, so a build starting from it" +
			" re-runs its base every time")
	}

	if got.Layer != (ir.NodeID{}) {
		t.Errorf("the empty base produced layer %v, and it produces none", got.Layer)
	}
}
