package layer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// A capture hands back the manifest for what it captured, from the same walk.
//
// **Because the walk has already read everything.** Capturing a layer reads
// every file to digest it, and then throws the per-file digests away; anything
// that wants them later - a peer authenticating a fragment, a copy deciding
// whether a file differs, a re-push asking what changed - pays for a second walk
// of a tree that has just been walked. Measured on a real 898 MB layer: the walk
// is 463 ms and the manifest it could have kept is 960 KB, which is 0.107% of
// the layer.
//
// The same argument the `Marked` note already makes one field over, where not
// writing it down cost 7.1 seconds of an 8 second build (E561).
func TestACaptureCanHandBackItsManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "sub"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	for at, body := range map[string]string{
		"a.txt":     "first",
		"sub/b.txt": "second",
	} {
		err = os.WriteFile(filepath.Join(root, at), []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	c, m, err := layer.TakeManifested(root)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	if len(m) == 0 {
		t.Fatal("the capture handed back no manifest")
	}

	// **The property that makes it worth storing.** A manifest attests to the
	// layer it came from: its identity must be the layer's, or it authenticates
	// nothing and a fragment checked against it is refused.
	if got := layer.ManifestID(m); got != c.ID {
		t.Errorf("the manifest attests to %v, the capture is %v", got, c.ID)
	}

	// And it is the same bytes the standalone walk produces, so nothing has two
	// answers about what a layer contains.
	want, err := layer.Manifest(root)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(m, want) {
		t.Errorf("the captured manifest is %d bytes and Manifest() gives %d",
			len(m), len(want))
	}
}

// An empty tree still gets a manifest, and it still attests.
func TestAnEmptyCaptureStillHasAManifest(t *testing.T) {
	t.Parallel()

	c, m, err := layer.TakeManifested(t.TempDir())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	if layer.ManifestID(m) != c.ID {
		t.Error("an empty layer's manifest does not attest to it")
	}
}
