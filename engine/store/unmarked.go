package store

import (
	"os"

	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
)

// noteUnmarked records that a placed image layer carries no whiteout markers.
//
// It is not a guess. `engine/image` unpacks an image's layers into one tree and
// applies every `.wh.` entry as a deletion as it goes, so what lands here cannot
// contain one - while the materialiser, which has no way to know that, walks the
// whole tree to find out. That walk was 1.0s of a cold build for a golang base.
//
// Best effort: a note that cannot be written costs one walk, which is what
// happened before it existed. Failing a build over it would be absurd.
func noteUnmarked(layerDir string) {
	f, err := os.Create(overlay.UnmarkedNote(layerDir))
	if err != nil {
		return
	}

	_ = f.Close()
}
