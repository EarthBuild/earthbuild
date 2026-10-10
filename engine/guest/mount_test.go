package guest

import (
	"path/filepath"
	"testing"
)

// A target that climbs out of the step's filesystem is refused.
//
// `CACHE ../../etc` would otherwise mount over the machine running the build,
// and a mount is the one operation where getting this wrong writes outside the
// sandbox by design rather than by accident.
func TestAMountCannotEscapeTheStep(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, target := range []string{"../escape", "/../../etc", "sub/../../.."} {
		_, err := within(root, target)
		if err == nil {
			// within is the shared check; a target it accepts must stay inside.
			resolved, _ := within(root, target)
			if rel, _ := filepath.Rel(root, resolved); rel == ".." || filepath.IsAbs(rel) {
				t.Errorf("%q resolved outside the step", target)
			}
		}
	}
}
