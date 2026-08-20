//go:build linux

package main

import (
	"fmt"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
)

// newMaterialiser returns the real thing: overlayfs over the layer store.
//
// Layers and scratch are separate. The layer store arrives over a shared mount
// from the host, and overlayfs cannot use such a filesystem as an upper layer -
// it falls back to a read-only mount, and the step's first write fails with an
// error that names nothing about the cause. Scratch therefore lives on the
// guest's own filesystem, which also means a step cannot write into the shared
// cache it is reading.
func newMaterialiser(root, scratch string) (core.Materialiser, error) {
	m, err := overlay.NewSplit(root, scratch)
	if err != nil {
		return nil, fmt.Errorf("prepare the overlay materialiser (layers %s, scratch %s): %w",
			root, scratch, err)
	}

	return m, nil
}
