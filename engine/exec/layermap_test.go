package exec

import (
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// What a derivation produced is remembered, so it need not be recomputed.
//
// This is 𝔄 in miniature (green paper 2.3): a key names a derivation, and what
// it maps to is the digest of the result. The layer itself is filed under its
// contents; this is the only thing that knows the two are related.
func TestWhatADerivationProducedIsRemembered(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	key := ir.NodeID{1, 2, 3}
	want := ir.NodeID{9, 8, 7}

	if _, ok := layerNamed(store, key); ok {
		t.Fatal("an empty store remembered something")
	}

	rememberLayer(store, key, want)

	got, ok := layerNamed(store, key)
	if !ok {
		t.Fatal("remembered and then not found")
	}

	if got != want {
		t.Errorf("remembered %v and read back %v", want, got)
	}
}

// A record that cannot be read is not an answer.
//
// It is a cache of a pure function, so a lost or corrupt record costs a
// recomputation and can never produce a wrong layer - but only if unreadable is
// reported as absent rather than as a zero id, which names the empty layer and
// names it confidently.
func TestAnUnreadableRecordIsNotAnAnswer(t *testing.T) {
	t.Parallel()

	store := t.TempDir()
	key := ir.NodeID{4, 5, 6}

	rememberLayer(store, key, ir.NodeID{1})

	// Truncated on disk, which is what an interrupted write leaves.
	err := writeFileForTest(store, key, "not a digest")
	if err != nil {
		t.Fatal(err)
	}

	if got, ok := layerNamed(store, key); ok {
		t.Errorf("a corrupt record answered %v", got)
	}
}
