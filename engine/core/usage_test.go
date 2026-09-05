package core_test

import (
	"context"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A build totals what its steps spent, and maxes what they peaked.
//
// Two different quantities: CPU adds up - two steps each spending a second cost
// the machine two - and peak memory does not, because two steps each peaking at
// a gigabyte, one after the other, never needed two (E467).
func TestABuildSumsCPUAndMaxesMemory(t *testing.T) {
	t.Parallel()

	g, _ := fan(2)

	s := &core.Scheduler{
		Workers:  []core.Worker{{ID: "w", IsInvoker: true}},
		Executor: spender{cpu: time.Second, rss: 1 << 30},
		Blobs:    allBlobs{},
	}

	_, err := s.Run(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}

	// Four steps in this graph - a base, two leaves and a merge - and every one
	// of them spends a second here.
	if s.Stats.CPU < 2*time.Second {
		t.Errorf("the build spent %v of CPU across steps that spent a second each",
			s.Stats.CPU)
	}

	if s.Stats.MaxRSS != 1<<30 {
		t.Errorf("peak memory is %d, and no step peaked above a gigabyte",
			s.Stats.MaxRSS)
	}
}

// spender runs nothing and reports the same usage every time.
type spender struct {
	cpu time.Duration
	rss uint64
}

func (e spender) Run(
	_ context.Context, n *ir.Node, _ core.Worker, _ []ir.NodeID, _ [][]ir.NodeID,
) (core.Result, error) {
	return core.Result{
		Layer: n.ID(), Captured: true, CPU: e.cpu, MaxRSS: e.rss,
	}, nil
}
