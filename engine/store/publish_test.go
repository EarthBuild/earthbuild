package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// staged builds a tree beside the layers directory, as every caller does.
func staged(t *testing.T, root, name, content string) string {
	t.Helper()

	err := os.MkdirAll(filepath.Join(root, "layers"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	at := filepath.Join(root, "layers", name)

	err = os.MkdirAll(at, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(at, "f"), []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return at
}

// A published tree is the layer, under its own name.
func TestPublishFilesAStagedTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.NodeID{1}

	err := Publish(root, id, staged(t, root, ".incoming-1", "one"))
	if err != nil {
		t.Fatal(err)
	}

	if !LayerStore(root).Has(id) {
		t.Fatal("a published tree is not the layer it was published as")
	}
}

// Losing the race to file a layer is success.
//
// Two builds fetching the same input both find it absent, both stage, and the
// loser's rename lands on a directory that now exists. The id names the
// content, so the winner filed the same bytes and checked them exactly as hard
// (green paper §5.3) - a step that got its input perfectly well once reported
// that it could not (E347).
func TestPublishingOverAnExistingLayerSucceeds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.NodeID{2}

	err := Publish(root, id, staged(t, root, ".incoming-winner", "same"))
	if err != nil {
		t.Fatal(err)
	}

	err = Publish(root, id, staged(t, root, ".incoming-loser", "same"))
	if err != nil {
		t.Fatalf("losing a race to file a layer was reported as a failure: %v", err)
	}

	// The winner's tree, untouched: publishing never replaces what is there.
	b, err := os.ReadFile(filepath.Join(LayerStore(root).Path(id), "f"))
	if err != nil {
		t.Fatal(err)
	}

	if string(b) != "same" {
		t.Fatalf("the layer holds %q, so the loser overwrote the winner", b)
	}
}

// A publish that cannot happen says which layer and where.
func TestPublishingWhatIsNotThereIsDiagnosed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := ir.NodeID{3}

	err := Publish(root, id, filepath.Join(root, "layers", ".never-staged"))
	if err == nil {
		t.Fatal("publishing a tree that does not exist was reported as success")
	}

	for _, want := range []string{id.String(), root} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the diagnosis does not name %s:\n  %v", want, err)
		}
	}
}
