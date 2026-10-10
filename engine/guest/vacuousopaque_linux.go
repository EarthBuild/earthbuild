//go:build linux

package guest

import (
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// opaqueAttrs are the names overlayfs marks a directory opaque under. Which one
// is in use depends on whether the mount was made with `userxattr`.
var opaqueAttrs = []string{"trusted.overlay.opaque", "user.overlay.opaque"}

// dropVacuousOpaque takes the opaque mark off directories in a delta that no
// lower layer has.
//
// **An opaque mark is a statement about one stack.** `mkdir d` in an overlay
// upper produces an opaque directory whether or not a lower has `d`: the kernel
// must guarantee the new directory reads as empty. Where a lower does have it,
// that mark is a deletion and has to survive. Where no lower has it, the mark
// decides nothing here - and a captured layer is content-addressed, so it will
// be stacked over lowers it was never created against, where the same mark hides
// a directory it was never about. `COPY` into a `WORKDIR` inherited from a
// shared base did exactly that, and destroyed what the destination already held
// (E704).
//
// Walked on the delta, not through the mount: overlayfs hides its own
// `trusted.overlay.*` attributes from the merged view, so a removal there finds
// nothing to remove.
//
// Best effort per directory, and deliberately so: a filesystem without extended
// attributes, or one that will not let this process write them, is not a reason
// to fail a step whose output is otherwise complete. Failing to drop a mark
// costs the merge that E704 describes; failing the step costs the build.
func dropVacuousOpaque(delta string, hasBelow func(string) bool) {
	if delta == "" || hasBelow == nil {
		return
	}

	_ = filepath.WalkDir(delta, func(p string, d fs.DirEntry, err error) error {
		// An entry this cannot read is not a step to fail: the walk carries on
		// and any mark on it stays, which is the safe direction - a mark left
		// alone costs the merge E704 describes, a failed step costs the build.
		if err != nil {
			return nil //nolint:nilerr // deliberate: see above
		}

		if !d.IsDir() {
			return nil
		}

		rel, relErr := filepath.Rel(delta, p)
		if relErr != nil {
			return nil //nolint:nilerr // a path outside the delta is not ours to touch
		}

		if rel == "." {
			return nil
		}

		if hasBelow(rel) {
			return nil
		}

		for _, at := range opaqueAttrs {
			_ = unix.Lremovexattr(p, at)
		}

		return nil
	})
}
