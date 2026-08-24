//go:build linux

package overlay

import (
	"os"
	"path/filepath"
	"testing"
)

// A layer is scanned for whiteout markers once, not once per step.
//
// The scan walks the whole layer, and the cache above it held only the result of
// a *translation* - so a layer with no markers, which is nearly all of them, was
// walked again on every materialise. On a golang base that was 0.54s of every
// step's 0.58s, which is the whole of this engine's per-step cost against
// buildkit (E529).
//
// The test changes the layer between the two calls, which a real layer cannot
// do: they are immutable and content-addressed, and that is exactly what makes
// remembering the answer sound. A second call that notices the change is a
// second call that walked the tree.
func TestALayerIsScannedForMarkersOnce(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "ordinary"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	tr := &translator{dir: t.TempDir(), done: map[string]string{}}

	first, err := tr.use(src, "layer-1")
	if err != nil {
		t.Fatalf("first use: %v", err)
	}

	if first != src {
		t.Fatalf("a layer with no markers was translated to %q, want %q", first, src)
	}

	// Only a rescan can see this.
	if err := os.WriteFile(filepath.Join(src, ".wh.gone"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := tr.use(src, "layer-1")
	if err != nil {
		t.Fatalf("second use: %v", err)
	}

	if second != first {
		t.Errorf("the layer was walked a second time: got %q, want the remembered %q", second, first)
	}
}

// The answer outlives the process that worked it out.
//
// The memo above the scan is per materialiser, and the materialiser is the guest
// daemon - which the idle timeout stops after 30 minutes. So the first build of
// a session walked the base again, 0.6s, every time. The positive answer was
// already durable: a translated layer is a directory on disk that the next
// process finds. Only "this layer has no markers" was being forgotten (E530).
func TestTheAnswerSurvivesTheProcessThatFoundIt(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	shared := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "ordinary"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	first, err := (&translator{dir: shared, done: map[string]string{}}).use(src, "layer-2")
	if err != nil {
		t.Fatalf("first use: %v", err)
	}

	// Only a rescan can see this, and a rescan is what a second process was
	// doing.
	if err := os.WriteFile(filepath.Join(src, ".wh.gone"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// A different translator over the same directory: a new guest, same VM.
	second, err := (&translator{dir: shared, done: map[string]string{}}).use(src, "layer-2")
	if err != nil {
		t.Fatalf("second use: %v", err)
	}

	if second != first {
		t.Errorf("a second process walked the layer again: got %q, want %q", second, first)
	}
}

// A note in the store is believed, and is the only note a fresh VM has.
//
// Whoever placed the layer knew: an image is flattened as it is unpacked and
// every `.wh.` entry applied as a deletion there, so a placed image cannot carry
// one. CI gets a new VM per build, so the guest's own notes are always absent
// and the scan was always paid (E531).
func TestAStoreNoteIsBelieved(t *testing.T) {
	t.Parallel()

	store := t.TempDir()

	src := filepath.Join(store, "layer-3")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}

	// Only a scan can see this, and the note says no scan is needed.
	err := os.WriteFile(filepath.Join(src, ".wh.gone"), nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(UnmarkedNote(src), nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got, err := (&translator{dir: t.TempDir(), done: map[string]string{}}).use(src, "layer-3")
	if err != nil {
		t.Fatalf("use: %v", err)
	}

	if got != src {
		t.Errorf("the store's note was ignored and the layer walked: got %q, want %q", got, src)
	}
}
