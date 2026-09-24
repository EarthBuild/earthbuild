//go:build !linux

package guest

import (
	"context"
	"errors"
)

// resolveStacks refuses off Linux, where nothing can be materialised.
func (s *Server) resolveStacks(_ context.Context, mounts []Mount) ([]Mount, func(), error) {
	for _, m := range mounts {
		if len(m.Stack) > 0 {
			return nil, nil, errors.New("a bound view of a stage needs Linux: it is assembled with overlayfs")
		}
	}

	return mounts, func() {}, nil
}
