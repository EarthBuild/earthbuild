package guest

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// ownWrites reports whether a path the step read was one the step itself made.
//
// **`printf > f && cat f` is everywhere**, and the read is real: the tracer sees
// an `openat` and records the path as an input. A base cannot contain a file the
// step has just created, so the prediction naming it is stale on every later
// build - three of six in this repository's own build (E696).
//
// **The dangerous half is the other one.** `sed -i` on a base file reads and
// writes the same path, and there the read *is* an input: dropping it is a false
// hit, which I3 forbids, and a false hit is worse than the miss being fixed.
//
// What tells them apart is not the delta - both are in it - but the base. A path
// the step wrote that is not below it was made by the step; one that is below it
// was read from there, whatever happened afterwards.
//
// Ordered so the common answer is cheapest: most read paths are not in the delta
// at all, and one failed stat settles them without touching the base.
func ownWrites(root string, base []ir.NodeID, delta string) func(string) bool {
	st := store.DirStore(root)

	// Where each layer of the base lives, resolved once rather than per path.
	below := make([]string, 0, len(base))
	for _, id := range base {
		below = append(below, st.LayerPath(id))
	}

	return func(rel string) bool {
		rel = strings.TrimPrefix(filepath.Clean("/"+rel), "/")
		if rel == "" || delta == "" {
			return false
		}

		_, err := os.Lstat(filepath.Join(delta, rel))
		if err != nil {
			// Not something this step wrote, so not its own output whatever
			// else it is.
			return false
		}

		for _, dir := range below {
			_, err := os.Lstat(filepath.Join(dir, rel))
			if err == nil {
				// Below it as well: the step edited what the base held, and the
				// read it made was of the base.
				return false
			}
		}

		return true
	}
}
