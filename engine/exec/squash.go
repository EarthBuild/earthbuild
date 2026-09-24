package exec

import (
	"context"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// Squash implements core.Squasher: it builds the layer a flattened stack names.
//
// Φ replaces a range of the stack with one identity so what remains can be
// mounted (green paper 4.8), and that identity is a directory somebody has to
// make. Nothing made it: the scheduler flattened, recorded the decision, and
// handed the executor a name that had never existed. The threshold was set at
// 480 and the mount fails at about 90, so Φ had never fired and the defect was
// latent rather than daily (E50).
func (e *Executor) Squash(ctx context.Context, into ir.NodeID, rng []ir.NodeID) error {
	// **Where the store is.** A squash reads every layer in the range and
	// writes a new one, which is the largest thing this engine does to a store
	// and the last thing that could sensibly be done from outside it. A guest
	// that is up owns its store as far as this is concerned, so it does the
	// work; a build that has not started one flattens here, which is what every
	// backend without a machine does and always will (E557).
	//
	// The guest that is *already* running, never one started for this. A
	// flatten can be asked for by an export on a build whose every step was a
	// cache hit, and booting a machine to merge directories the host can see
	// would put a VM back on the path this engine spent a quarter taking it off
	// (E537). Where there is no guest, the host flattens, which is what every
	// backend without a machine does and always will.
	c := e.startedClient()
	if c != nil {
		return c.Squash(ctx, into, rng)
	}

	return store.DirStore(e.sb.StoreDir()).Squash(ctx, into, rng)
}
