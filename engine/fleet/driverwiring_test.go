package fleet

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// The driver states its own capacity and knows how big its layers are.
//
// **Two fields the probe set and the product did not.** E317 prices a transfer
// and E321 stops a driver keeping work it cannot run - both were measured in the
// probe, which sets `Room` and `Sizes` by hand, and neither was wired into
// `fleet.Driver`. In a real build:
//
//   - `Room` of zero means "as many as arrive", so `waves` answers one however
//     much is running and the driver looks infinitely parallel - exactly the
//     denominator E321 was written to correct;
//   - `Sizes` of nil means every layer no step produced - the base image, every
//     time - is priced at zero bytes, so the transfer term in E317's comparison
//     is always absent.
//
// *Failure class: a fault fixed where it was found rather than where it lives.*
// Same as E329, one file away.
func TestADriverStatesItsCapacityAndKnowsItsSizes(t *testing.T) {
	t.Parallel()

	d := &Delegating{}

	wire(d, 4, &Layers{Root: t.TempDir()})

	if d.Room != 4 {
		t.Errorf("a driver with room for four says %d, and a driver that says"+
			" nothing finishes every step in one wave (E330)", d.Room)
	}

	if d.Sizes == nil {
		t.Fatal("a driver cannot say how big its own layers are, so every base" +
			" is priced at nothing (E330)")
	}

	if got := d.Sizes(ir.NodeID{9}); got != 0 {
		t.Errorf("a layer this driver does not have measured %d bytes", got)
	}
}

// A driver measures a layer once, however many steps stand on it.
//
// A layer is a directory of thousands of files and every delegable step asks
// what it weighs. Walking it per step would put a stat storm in front of a
// mechanism whose whole purpose is to avoid moving bytes.
func TestALayerIsMeasuredOnce(t *testing.T) {
	t.Parallel()

	held := &countingLayers{Layers: &Layers{Root: t.TempDir()}}

	d := &Delegating{}

	wire(d, 2, held)

	for range 5 {
		_ = d.Sizes(ir.NodeID{9})
	}

	if held.asked != 1 {
		t.Errorf("a layer was measured %d times for five steps", held.asked)
	}
}

// countingLayers counts how often it is asked to measure something.
type countingLayers struct {
	*Layers

	asked int
}

func (c *countingLayers) Size(id ir.NodeID) (int64, bool) {
	c.asked++

	return c.Layers.Size(id)
}
