package guest

import "testing"

// layerStoreForTest is a layer store with nothing in it.
//
// The mount tests here are about cache mounts and sandbox paths, none of which
// resolve against the layer store - so an empty directory is the honest value:
// present, because bindMounts takes one, and unused, because these mounts do
// not name a layer. A bound view's own test supplies a store with a layer in it.
func layerStoreForTest(t *testing.T) string {
	t.Helper()

	return t.TempDir()
}
