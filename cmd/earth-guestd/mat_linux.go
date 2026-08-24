//go:build linux

package main

import (
	"fmt"
	"os"

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
// **Where a stack can be mounted, which is not always where we were told to put
// it.** A container's root is overlayfs and overlayfs will not stack on
// overlayfs, so a guest whose scratch is on the step's own root cannot
// materialise its first base - it fails with `invalid argument`, which names
// nothing about the cause. That is every containerised CI runner, including this
// repository's own.
//
// `Mountable` has known the way out of that since before anything used it: try
// where the caller asked, then a tmpfs, which overlayfs will stack on. It was
// reached only from tests, so the escape the engine wrote for itself was the one
// thing production never took (E634).
//
// The relocation is said out loud rather than done quietly. A scratch on tmpfs
// is memory, so a step that writes gigabytes now writes them to RAM, and an
// operator who is not told will find that out from the OOM killer instead of
// from us (I11).
func newMaterialiser(root, scratch string) (core.Materialiser, func(), error) {
	at, cleanup, err := overlay.Mountable(scratch)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"find somewhere to mount this step's filesystem (asked for %s): %w", scratch, err)
	}

	if at != scratch {
		fmt.Fprintf(os.Stderr,
			"earth: %s cannot host an overlay mount, so this step's scratch is %s\n"+
				"  that is memory rather than disk: a step writing more than this"+
				" machine has free will be killed rather than slowed\n",
			scratch, at)
	}

	m, err := overlay.NewSplit(root, at)
	if err != nil {
		cleanup()

		return nil, nil, fmt.Errorf("prepare the overlay materialiser (layers %s, scratch %s): %w",
			root, at, err)
	}

	return m, cleanup, nil
}
