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
	return store.DirStore(e.sb.StoreDir()).Squash(ctx, into, rng)
}
