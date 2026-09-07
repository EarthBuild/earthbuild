package store_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/store"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// TestADeclarationIsTheSameWhetherReadOrHandedOver.
//
// **The store is moving onto the device the guest owns, and a host cannot read
// what it does not have.** `Declaration` answers from a config sidecar beside
// the layer, which is fine while both sides see the same directory and is the
// assumption that move removes.
//
// The host does not need to read it. It fetched that configuration over the
// network a moment earlier - `PullApart` returns it - so the conversion can be
// done from the value in hand. What must not differ is the answer: a stack
// element derived one way and looked up the other would be two different
// elements for one image (§3.2a).
func TestADeclarationIsTheSameWhetherReadOrHandedOver(t *testing.T) {
	t.Parallel()

	for _, cfg := range []ocispec.ImageConfig{
		{},
		{Env: []string{"PATH=/usr/local/bin:/usr/bin", "LANG=C.UTF-8"}},
		{WorkingDir: "/src", User: "1000:1000"},
		{Entrypoint: []string{"/entry"}, Cmd: []string{"--serve"}},
		{
			Env: []string{"A=$B"}, WorkingDir: "/w", User: "root",
			Entrypoint: []string{"/e"}, Cmd: []string{"c"},
		},
	} {
		root := t.TempDir()
		id := ir.NodeID{7}

		// A layer with that configuration beside it, which is what a pull leaves.
		at := filepath.Join(root, "layers")

		err := os.MkdirAll(filepath.Join(at, id.String()), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		b, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(at, id.String())+store.ConfigSuffix, b, 0o600)
		if err != nil {
			t.Fatal(err)
		}

		read := store.DirStore(root).Declaration(id)
		handed := store.DeclarationOf(cfg)

		if read != handed {
			t.Errorf("config %+v declares %v when read and %v when handed over",
				cfg, read, handed)
		}
	}
}
