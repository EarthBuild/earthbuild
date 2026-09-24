//go:build linux

package guest

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// resolveStacks assembles each bound view of an earlier result and rewrites the
// mount to the path it now sits at.
//
// A view of a stage is that stage's whole filesystem, which is a stack of
// layers and not one of them - so it has to be materialised before anything can
// be bound. The same materialiser that builds a step's own base builds this,
// which is what keeps the two definitions of "a stage's filesystem" from
// drifting apart.
//
// Rewritten to a plain path rather than given its own case in bindMounts: once
// assembled it *is* a path on this machine, which the mount code already knows
// how to bind. The returned function releases the handles, and must be called
// after the step rather than after the binding - the mount reads through it.
func (s *Server) resolveStacks(ctx context.Context, mounts []Mount) ([]Mount, func(), error) {
	var held []core.Handle

	release := func() {
		for _, h := range held {
			_ = h.Release()
		}
	}

	out := make([]Mount, len(mounts))
	copy(out, mounts)

	for i := range out {
		if len(out[i].Stack) == 0 {
			continue
		}

		stack := make([]ir.NodeID, 0, len(out[i].Stack))

		for _, raw := range out[i].Stack {
			id, err := ir.ParseNodeID(raw)
			if err != nil {
				release()

				return nil, nil, fmt.Errorf("bound view at %s: %w", out[i].Target, err)
			}

			stack = append(stack, id)
		}

		h, err := s.Mat.Materialise(ctx, stack)
		if err != nil {
			release()

			return nil, nil, fmt.Errorf("assemble the view bound at %s: %w", out[i].Target, err)
		}

		held = append(held, h)

		// Sandbox is "a path on this machine", which is exactly what an
		// assembled root is. Stack is cleared so nothing downstream tries to
		// assemble it twice.
		out[i].Sandbox = filepath.Join(h.Root(), filepath.Clean("/"+out[i].Sub))
		out[i].Stack, out[i].Sub = nil, ""
	}

	return out, release, nil
}
