//go:build !linux

package main

import (
	"errors"

	"github.com/EarthBuild/earthbuild/engine/core"
)

// newMaterialiser refuses off Linux rather than substituting something weaker.
//
// The guest's whole purpose is to assemble layers with overlayfs; a build that
// silently ran without layering would produce results that look like layers and
// are not.
func newMaterialiser(_, _ string) (core.Materialiser, error) {
	return nil, errors.New("earth-guestd requires Linux: it assembles layers with overlayfs")
}
