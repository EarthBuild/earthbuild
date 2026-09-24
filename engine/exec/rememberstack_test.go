package exec

import (
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// TestAStackIsRememberedEvenWhereNothingHasMadeTheDirectory.
//
// **A best-effort write that always fails is not best effort, it is dead code.**
// The note lives beside the shared image-cache entry, and the host's own pull
// creates that directory on its way past - so the write worked, and nobody
// noticed it depended on somebody else having been there first.
//
// The guest-unpacking path fetches blobs instead and never touches the image
// cache, so every build wrote its note into a directory that was not there, the
// error was discarded as designed, and the cheap path - "these layers are
// already unpacked, use them" - could never fire.
//
// Found by looking for the notes and finding none, after a comparison that read
// two empty files as agreement.
func TestAStackIsRememberedEvenWhereNothingHasMadeTheDirectory(t *testing.T) {
	t.Parallel()

	shared := filepath.Join(t.TempDir(), "imagecache", "some-key")

	want := []ir.NodeID{{1}, {2}, {3}}

	rememberImageStack(shared, want)

	got, ok := imageStackNamed(shared)
	if !ok {
		t.Fatal("the stack was not remembered, so every build re-fetches an" +
			"\n  image it has already unpacked")
	}

	if len(got) != len(want) {
		t.Fatalf("remembered %d ids, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("id %d came back as %v, want %v", i, got[i], want[i])
		}
	}
}

// TestAnEmptyLayerCountsAsUnpacked.
//
// **An empty directory is a layer.** Images ship them - `golang:1.26-alpine`
// stacks five and the topmost holds nothing - and so do steps that write
// nothing at all.
//
// Asking whether a layer is *populated* answers a different question, and
// answers it wrongly: it reads "there, and empty" as "not there". One such
// layer in a stack made the whole remembered stack look absent, so a warm
// build of `golang:1.26-alpine` re-fetched and re-unpacked all five layers
// every time - 8.1s against the 0.2s of a stack it had already got.
//
// `Has` is the question that was meant, and it says so itself: partial
// commits are prevented by renaming into place, not by guessing from
// contents. So emptiness carries no information about presence.
func TestAnEmptyLayerCountsAsUnpacked(t *testing.T) {
	t.Parallel()

	st := store.DirStore(t.TempDir())

	staged, err := st.Staging(".empty-")
	if err != nil {
		t.Fatal(err)
	}

	id, err := st.Place(staged)
	if err != nil {
		t.Fatalf("place an empty layer: %v", err)
	}

	if !st.Has(id) {
		t.Fatalf("the store does not hold the empty layer it just placed")
	}

	if !allPresent(st, []ir.NodeID{id}) {
		t.Error("a stack holding an empty layer is reported as not unpacked," +
			"\n  so every build re-fetches an image it already has")
	}
}
