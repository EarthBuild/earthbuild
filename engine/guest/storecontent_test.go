package guest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
	"github.com/EarthBuild/earthbuild/engine/store"
)

// The store answers what a layer holds, with its times excluded.
//
// The other half of asking rather than looking. Κₜ (green paper 4.5a) names a
// base by `contents(𝑏)`, and a store on a device the guest owns is not on the
// host's filesystem - so a host that folds the manifest itself reads nothing,
// derives no key, and the tier that exists for rebuilt bases never fires on the
// builds with the most to gain.
//
// A layer with no manifest is answered as unknown rather than as a zero: Κₜ
// treats absence as not-derivable, and a zero would be a content id two bases
// holding anything at all could share.
func TestTheStoreAnswersALayersContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// A tree, the layer it would be, and the manifest beside it.
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	took, err := layer.Take(tree)
	if err != nil {
		t.Fatal(err)
	}

	m, err := layer.Manifest(tree)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, "layers", took.ID.String()), 0o750); err != nil {
		t.Fatal(err)
	}

	store.NoteManifest(root, took.ID, m)

	unknown := ir.NodeID{2}

	c := pairWith(t, &guest.Server{LayerDir: root})

	got, err := c.StoreContent(context.Background(), []ir.NodeID{took.ID, unknown})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("asked about 2 layers and heard about %d - the reply must line"+
			" up with the question or the caller cannot tell which is which", len(got))
	}

	if got[0] != took.Content {
		t.Errorf("answered %v for a layer whose capture said %v", got[0], took.Content)
	}

	if got[1] != (ir.NodeID{}) {
		t.Errorf("answered %v for a layer with no manifest, where the zero id is"+
			" how 'unknown' is spelled", got[1])
	}
}
