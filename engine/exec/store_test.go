package exec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// The store is a thing with a surface, not a path everybody knows.
//
// Six capabilities reach into it host-side, each joining a path and walking. A
// store on a block device cannot be walked from the host at all, so each has to
// become an operation before the storage can move - and naming them is what
// makes that reviewable rather than a rewrite (E541).
//
// This test is the surface. A directory-backed store satisfies it today; a
// guest-backed one satisfies it in phase 2, and nothing above it has to know
// which it is holding.
func TestADirectoryStoreSatisfiesTheSurface(t *testing.T) {
	t.Parallel()

	var s Store = DirStore(t.TempDir())

	id := ir.NodeID{1, 2, 3}

	if s.Has(id) {
		t.Error("an empty store claims to hold a layer")
	}

	// A store says where it keeps a layer, because a caller that materialises
	// one still needs the path - and phase 2 is where that stops being true.
	if s.LayerPath(id) == "" {
		t.Error("the store cannot say where a layer lives")
	}
}

// What an image declares is asked of the store, not read from beside a file.
func TestTheStoreAnswersWhatAnImageDeclared(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	s := DirStore(root)
	id := ir.NodeID{9}

	at := filepath.Join(root, "layers", id.String())
	if err := os.MkdirAll(at, 0o750); err != nil {
		t.Fatal(err)
	}

	if got := s.Declaration(id); got != (ir.NodeID{}) {
		t.Errorf("a layer with no configuration declared %v", got)
	}

	b, err := json.Marshal(ocispec.ImageConfig{Env: []string{"PATH=/go/bin"}})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(at+configSuffix, b, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := s.Declaration(id); got == (ir.NodeID{}) {
		t.Error("a layer whose image declared an environment produced no declaration")
	}
}
