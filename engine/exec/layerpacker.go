package exec

import (
	"context"
	"io"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// LayerPacker is a sandbox that can hand one layer of its own store to the host.
//
// **Optional, because only some sandboxes need it.** Where the store is a
// directory the host shares, the host reads layers off its own disk and this is
// not implemented; where the store is a block device the guest holds open, it is
// the only way an image can be written at all.
//
// Named rather than asserted inline at the one call site, because an unnamed
// interface nothing implements is indistinguishable from one nothing needs -
// which is how `SAVE IMAGE` came to skip every layer of every image under the
// microVM backend while the branch that would have packed them sat unreachable.
type LayerPacker interface {
	PackLayer(ctx context.Context, id ir.NodeID, w io.Writer) error
}
