package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// aTree is a materialised layer with a fixed mtime.
//
// **Fixed, because ℓ_id includes timestamps** (§3.3a, I8). Two trees with the
// same bytes written a millisecond apart are two different layers by identity,
// which is correct and is not what an image unpacked twice looks like: an OCI
// layer carries its mtimes, so two machines unpacking the same layer stamp the
// same times and arrive at the same name. A test whose trees were stamped by the
// clock would be asserting that content addressing does not dedup, and would be
// right about the fixture and wrong about the system.
func aTree(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()

	at := filepath.Join(dir, "f")

	err := os.WriteFile(at, []byte(body), 0o600)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	fixed := time.Unix(1000000000, 0)

	err = os.Chtimes(at, fixed, fixed)
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}

	err = os.Chtimes(dir, fixed, fixed)
	if err != nil {
		t.Fatalf("stamp the directory: %v", err)
	}

	return dir
}

// A layer is filed under what is in it.
//
// Green paper §3.2: "a layer is a content-addressed filesystem delta", and
// §3.3a says ℓ_id is what is stored in 𝔄 and transferred between workers. A
// layer filed under the node id of the operation that produced it is filed under
// a name for the *derivation*, so a peer asking for it by name cannot check what
// arrives - which is E507, and the reason a fleet cannot share a base.
//
// RUN results were already filed this way; images and contexts were not, which
// is why some fetches across machines worked and bases never did.
func TestALayerIsFiledUnderWhatIsInIt(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	id, err := placeCaptured(store, aTree(t, "hello"))
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	at := filepath.Join(store, "layers", id.String())
	if _, err := os.Stat(at); err != nil {
		t.Fatalf("filed as %s and it is not there: %v", id, err)
	}

	// The name is a claim about the contents, so it must survive being checked.
	c, err := layer.TakeOwnedIn(at, layer.IDMap{}, layer.IDMap{}, nil)
	if err != nil {
		t.Fatalf("capture what was filed: %v", err)
	}

	if c.ID != id {
		t.Errorf("filed as %s and its contents capture to %s", id, c.ID)
	}
}

// The same layer files once.
//
// Two targets on the same base, or two machines unpacking one image, produce the
// same digest and the second is already there. Deduplication is not an
// optimisation here - it is what content addressing means, and a store that
// filed a second copy would be claiming the two are different.
//
// "The same layer" and "the same bytes" are not the same claim: identity
// includes mtimes, so this holds for trees that agree about those too. See
// aTree.
func TestTheSameLayerIsFiledOnce(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	first, err := placeCaptured(store, aTree(t, "same"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	second, err := placeCaptured(store, aTree(t, "same"))
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if first != second {
		t.Fatalf("identical trees filed as %s and %s", first, second)
	}

	entries, err := os.ReadDir(filepath.Join(store, "layers"))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Errorf("%d entries for one distinct layer", len(entries))
	}
}

// Different contents are different layers.
func TestDifferentTreesAreDifferentLayers(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	a, err := placeCaptured(store, aTree(t, "one"))
	if err != nil {
		t.Fatal(err)
	}

	b, err := placeCaptured(store, aTree(t, "two"))
	if err != nil {
		t.Fatal(err)
	}

	if a == b {
		t.Errorf("two different trees both filed as %s", a)
	}
}
