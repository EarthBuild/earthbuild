package layer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A narrowed capture and its manifest describe the same tree.
//
// **They did not, and nothing on a developer's machine could tell.** A capture
// is named by its digest and attested by its manifest, so the two describing
// different trees means the manifest attests to a layer that is not the one
// stored - which `store.TreeNodes` reports as a fold landing somewhere the
// entry does not name, and which makes a narrowed layer unservable over REAPI.
//
// The difference was ownership: the capture was taken with no id translation
// and the manifest with the caller's, so they agree exactly when the maps are
// empty. They are empty on macOS and on any test that does not pass one, and
// they are not empty inside a user namespace - which is where every real step
// runs (E313's territory, and I13's).
func TestANarrowedCaptureAndItsManifestAgree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, at := range []string{"out", "debris"} {
		if err := os.WriteFile(filepath.Join(dir, at), []byte(at), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A map that renames the id these files carry, as a user namespace does.
	uids, err := layer.ParseIDMap(strings.NewReader("0 100000 65536"))
	if err != nil {
		t.Fatal(err)
	}

	c, m, err := layer.TakeDeclaredInManifested(dir, nil, []string{"out"}, uids, uids)
	if err != nil {
		t.Fatal(err)
	}

	f := layer.NewFold()
	if !f.Add(m) {
		t.Fatal("the manifest this capture produced could not be folded")
	}

	if got := f.Tree().Root(); got != c.Content {
		t.Errorf("the manifest folds to %s and the capture is named %s"+
			"\n  the manifest attests to a tree that is not the one stored", got, c.Content)
	}
}
