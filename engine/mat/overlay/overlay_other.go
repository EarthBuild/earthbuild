//go:build !linux

// Package overlay is Linux-only: overlayfs does not exist elsewhere. The macOS
// materialiser runs inside the guest VM instead (earth-guestd), for the reason
// experiment E1b found - `container exec` accepts no mount options, so a
// running VM cannot have filesystems attached from outside.
package overlay

import (
	"context"
	"errors"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// ErrUnsupported reports that overlayfs is not available on this platform.
var ErrUnsupported = errors.New("overlayfs materialiser requires linux")

// ErrUnavailable exists off Linux so callers compile everywhere. Nothing
// returns it here: the platform is wrong, which is ErrUnsupported, and a
// machine cannot be unable to do something its kind never does.
var ErrUnavailable = errors.New("overlayfs cannot be mounted here")

// Available always reports the platform error off Linux.
func Available(string) error { return ErrUnsupported }

// Materialiser is the non-Linux stub.
type Materialiser struct{}

// New always fails off Linux, loudly rather than by degrading to something that
// looks like it works (green paper I10: refuse, never approximate).
func New(string) (*Materialiser, error) { return nil, ErrUnsupported }

// Materialise always fails off Linux.
func (*Materialiser) Materialise(context.Context, []ir.NodeID) (core.Handle, error) {
	return nil, ErrUnsupported
}
