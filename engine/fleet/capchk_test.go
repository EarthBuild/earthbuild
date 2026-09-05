package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// Whether a layer directory is named by what is in it.
//
// A diagnostic over a real store, and the one that settled E507. A fleet
// transport that files an arrival under the capture of its contents and checks
// that against the id it asked for is assuming these are the same namespace.
// They are not: a layer directory is named by its node id - the cache key the
// build asked for - and the capture of its contents is a different value
// entirely.
func TestIsTheDirectoryNameACaptureOfItsContents(t *testing.T) { //nolint:paralleltest // a real store
	root, want := os.Getenv("EARTH_PROBE_STORE"), os.Getenv("EARTH_PROBE_LAYER")
	if root == "" || want == "" {
		t.Skip("set EARTH_PROBE_STORE and EARTH_PROBE_LAYER")
	}

	c, err := layer.TakeOwnedIn(filepath.Join(root, "layers", want), layer.IDMap{}, layer.IDMap{}, nil)
	if err != nil {
		t.Fatalf("capture it: %v", err)
	}

	t.Logf("directory is named %s", want)
	t.Logf("its contents capture to %s", c.ID)
	t.Logf("content-only (no mtimes) %s", c.Content)

	if c.ID.String() != want {
		t.Errorf("the name is not a capture of the contents")
	}
}
