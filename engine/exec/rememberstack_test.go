package exec

import (
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
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
