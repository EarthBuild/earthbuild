package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/mat/overlay"
)

// An image this engine unpacked needs no scanning for whiteout markers.
//
// The unpacker applies every `.wh.` entry as a deletion and flattens the image
// into one tree, so the placed layer provably carries none - and the guest
// otherwise spends a full walk of the base rediscovering that. On a fresh VM the
// store's note is the only one there is (E531).
func TestPlacingAnImageRecordsThatItHasNoMarkers(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	layer := filepath.Join(store, "layers", "abc123")
	if err := os.MkdirAll(layer, 0o750); err != nil {
		t.Fatal(err)
	}

	noteUnmarked(layer)

	_, err := os.Stat(overlay.UnmarkedNote(layer))
	if err != nil {
		t.Fatalf("no note beside a layer that cannot carry markers: %v", err)
	}
}
