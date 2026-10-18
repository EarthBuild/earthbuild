package fleet

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// counting runs steps and remembers how many were running at once.
type counting struct {
	now  atomic.Int64
	most atomic.Int64
}

func (c *counting) Run(
	context.Context, *ir.Node, core.Worker, []ir.NodeID, [][]ir.NodeID,
) (core.Result, error) {
	n := c.now.Add(1)
	for {
		most := c.most.Load()
		if n <= most || c.most.CompareAndSwap(most, n) {
			break
		}
	}

	time.Sleep(20 * time.Millisecond)
	c.now.Add(-1)

	return core.Result{}, nil
}

// TestThisMachineTakesOnlyWhatItSaidItWould.
//
// **The scheduler's limit is now the fleet's width, so a machine has to bound
// itself.** It used to be the driver's core count, which bounded the driver by
// accident; with the build allowed as many steps in flight as the fleet has
// cores, nothing stopped every step that stayed local from starting at once on
// a machine with far fewer (E-F1).
//
// `Room` already said how many this machine runs at once and was used only for
// pricing a transfer. It is a bound now, which is what the name says.
func TestThisMachineTakesOnlyWhatItSaidItWould(t *testing.T) {
	t.Parallel()

	x := &counting{}
	d := &Delegating{Local: x, Room: 2}

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			_, _ = d.Run(t.Context(), &ir.Node{}, core.Worker{IsInvoker: true}, nil, nil)
		})
	}

	wg.Wait()

	if most := x.most.Load(); most > 2 {
		t.Errorf("%d steps ran here at once against Room 2, so a wide fleet"+
			" oversubscribes whichever machine keeps a step", most)
	}
}

// TestAMachineThatNamedNoRoomIsNotBounded. Zero has always meant "as many as
// arrive", and a driver with no executor of its own effectively has that.
func TestAMachineThatNamedNoRoomIsNotBounded(t *testing.T) {
	t.Parallel()

	x := &counting{}
	d := &Delegating{Local: x}

	var wg sync.WaitGroup

	for range 4 {
		wg.Go(func() {
			_, _ = d.Run(t.Context(), &ir.Node{}, core.Worker{IsInvoker: true}, nil, nil)
		})
	}

	wg.Wait()

	if most := x.most.Load(); most < 2 {
		t.Errorf("a machine that named no room ran %d at once, so zero has"+
			" started meaning one", most)
	}
}
