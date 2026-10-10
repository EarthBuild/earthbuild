package fleet

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A driver tells a worker how long this kind of step took last time.
//
// **A field declared, encoded, decoded and never set.**
// `Hints.EstimatedSeconds` has been on the wire since the protocol was written,
// and nothing anywhere filled it - so the only thing placement knew about a
// step's cost was `Bytes`, the size of its *inputs*. A base worth shipping for a
// ten-minute compile and one worth keeping for a two-second step were priced
// identically, against a fleet-wide average step (`Rate.Slots`).
func TestADriverSaysHowLongAStepTookLastTime(t *testing.T) {
	t.Parallel()

	sent := &capturing{}

	d := &Delegating{
		Local: local(core.Result{Layer: ir.NodeID{2}}),
		Fleet: sent,
		Cost:  func(*ir.Node) (time.Duration, bool) { return 90 * time.Second, true },
	}

	if _, err := d.Run(t.Context(), node(), core.Worker{ID: "w"}, nil, nil); err != nil {
		t.Fatal(err)
	}

	if got := sent.last().Hints.EstimatedSeconds; got != 90 {
		t.Errorf("the assignment says %ds, want 90", got)
	}
}

// Rounded to the nearest second, not truncated.
//
// A step measured at 1.6s is worth two seconds of somebody's transfer budget,
// and truncating loses the part that would have tipped the comparison. Under
// half a second reports nothing at all, which is "not worth pricing" rather than
// "instant" - the reading `Slots` already gives an unstated size.
func TestAnEstimateIsRoundedRatherThanTruncated(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		took time.Duration
		want int64
	}{
		{1600 * time.Millisecond, 2},
		{1400 * time.Millisecond, 1},
		{400 * time.Millisecond, 0},
		{0, 0},
	} {
		d := &Delegating{Cost: func(*ir.Node) (time.Duration, bool) { return c.took, c.took > 0 }}

		if got := d.estimated(node()); got != c.want {
			t.Errorf("%v estimated as %ds, want %d", c.took, got, c.want)
		}
	}
}

// A driver with no history says nothing rather than zero.
//
// Absence and "instant" must not flatten into each other: a step priced at
// nothing is one no transfer could ever be worth, which is not a degraded answer
// but an inverted one.
func TestADriverWithNoHistorySaysNothing(t *testing.T) {
	t.Parallel()

	if got := (&Delegating{}).estimated(node()); got != 0 {
		t.Errorf("a driver with no cost store estimated %ds", got)
	}

	none := &Delegating{Cost: func(*ir.Node) (time.Duration, bool) { return 0, false }}
	if got := none.estimated(node()); got != 0 {
		t.Errorf("a class with no history estimated %ds", got)
	}
}

// capturing is a transport that keeps the last assignment it was handed.
type capturing struct {
	mu   sync.Mutex
	seen Assignment
}

func (c *capturing) Assign(_ context.Context, a Assignment) (Reply, error) {
	c.mu.Lock()
	c.seen = a
	c.mu.Unlock()

	return Reply{Version: Version, Layer: ir.NodeID{2}}, nil
}

func (c *capturing) Workers() int { return 1 }

func (c *capturing) last() Assignment {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.seen
}
