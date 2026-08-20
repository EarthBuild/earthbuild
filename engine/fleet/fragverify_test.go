package fleet_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/fleet"
	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// An honest fragment, with its manifest, is kept.
func TestAFragmentWithItsManifestIsKept(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := aLayer(t, root)

	m, packed := fragmentAndManifest(t, root, id, []string{"etc/hosts"})

	mine := &fleet.Fragments{Root: t.TempDir()}

	err := mine.PutVerified(id, []string{"etc/hosts"}, m, bytes.NewReader(packed))
	if err != nil {
		t.Fatalf("an honest fragment was refused: %v", err)
	}

	if !mine.Has(id, []string{"etc/hosts"}) {
		t.Error("it was not kept")
	}
}

// A manifest that does not hash to the layer is refused before anything else.
//
// **The check the whole scheme rests on.** The manifest is what makes a
// fragment's contents trustworthy (E284), and a manifest is only trustworthy
// because it hashes to the name the layer is already known by. A peer that sent
// a manifest of its own devising could then authenticate anything it liked.
//
// Checked first, and cheaply: it is one hash of two megabytes, against unpacking
// a fragment and walking it.
func TestAManifestThatIsNotThisLayersIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := aLayer(t, root)

	// A manifest of somebody else's tree, with a fragment to match - internally
	// consistent, and not this layer.
	other := t.TempDir()
	otherID := aLayerWithContent(t, other, "an entirely different image")

	m, packed := fragmentAndManifest(t, other, otherID, []string{"file"})

	mine := &fleet.Fragments{Root: t.TempDir()}

	err := mine.PutVerified(id, []string{"file"}, m, bytes.NewReader(packed))
	if err == nil {
		t.Fatal("a manifest of another layer authenticated a fragment of this one")
	}

	if mine.Has(id, []string{"file"}) {
		t.Error("and it was kept")
	}
}

// An honest manifest with a tampered fragment is refused.
//
// The other half: the manifest is right, and the bytes are not what it says they
// are. This is the case a peer with a corrupt disk produces, and the case an
// attacker who cannot forge a manifest falls back to.
func TestAnHonestManifestDoesNotExcuseATamperedFragment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := aLayer(t, root)

	m, _ := fragmentAndManifest(t, root, id, []string{"etc/hosts"})

	// A fragment with the right shape and the wrong contents.
	fake := t.TempDir()

	err := os.MkdirAll(filepath.Join(fake, "etc"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(fake, "etc", "hosts"),
		[]byte("127.0.0.1 somewhere-else\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	err = layer.PackPaths(fake, &buf, []string{"etc/hosts"})
	if err != nil {
		t.Fatal(err)
	}

	mine := &fleet.Fragments{Root: t.TempDir()}

	err = mine.PutVerified(id, []string{"etc/hosts"}, m, &buf)
	if err == nil {
		t.Fatal("a tampered fragment was kept under an honest manifest")
	}

	if mine.Has(id, []string{"etc/hosts"}) {
		t.Error("and it was kept")
	}
}

// A refused fragment leaves nothing behind.
func TestARefusedFragmentLeavesNothing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	id := aLayer(t, root)

	m, _ := fragmentAndManifest(t, root, id, []string{"etc/hosts"})

	mine := &fleet.Fragments{Root: t.TempDir()}

	err := mine.PutVerified(id, []string{"etc/hosts"}, m,
		bytes.NewReader([]byte("not a pack at all")))
	if err == nil {
		t.Fatal("rubbish was accepted")
	}

	entries, err := os.ReadDir(filepath.Join(mine.Root, "fragments", id.String()))
	if err == nil && len(entries) != 0 {
		t.Errorf("%d directory(ies) left behind", len(entries))
	}
}

func fragmentAndManifest(
	t *testing.T, root string, id ir.NodeID, want []string,
) ([]byte, []byte) {
	t.Helper()

	at := filepath.Join(root, "layers", id.String())

	m, err := layer.Manifest(at)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	err = layer.PackPaths(at, &buf, want)
	if err != nil {
		t.Fatal(err)
	}

	return m, buf.Bytes()
}
