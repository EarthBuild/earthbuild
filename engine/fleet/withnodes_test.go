package fleet

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A driver serves the nodes beside its layers.
//
// **The other half of `fleet.Nodes`, and it was missing.** A worker serves its
// store's `nodes/`; the driver served only layers - and the driver is the
// machine that holds the helper module and the units of every cache it filled.
// So a worker asking for one got "no peer served it" from the one peer that
// certainly had it.
func TestAStoreCanAlsoServeItsNodes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	body := []byte("a unit, or the module that knows what a unit is")
	id := ir.DigestOf(body)

	at := filepath.Join(root, "nodes", id.String())
	if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(at, body, 0o600); err != nil {
		t.Fatal(err)
	}

	s := WithNodes(&Layers{Root: root}, root)

	if !s.Has(id) {
		t.Fatal("a node this driver holds is not claimed, so nobody will ask for it")
	}

	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("get a held node: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Errorf("served %q, want %q", got, body)
	}
}

// And it is still the store it was: what it keeping, it keeps.
//
// The wrapper embeds rather than reimplements, so this is guarding against
// somebody replacing the embedding with three explicit methods and dropping the
// fourth - which on a driver would be a store that serves and never keeps.
func TestAStoreServingNodesStillKeepsLayers(t *testing.T) {
	t.Parallel()

	under := &keeping{held: map[ir.NodeID][]byte{}}
	s := WithNodes(under, t.TempDir())

	id, _, err := s.Put(bytes.NewReader([]byte("whatever a layer is to the store under this")))
	if err != nil {
		t.Fatalf("put through a store that also serves nodes: %v", err)
	}

	if !s.Has(id) {
		t.Error("what was put is not held, so wrapping a store lost its contents")
	}

	if _, err := s.Get(id); err != nil {
		t.Errorf("what was put cannot be read back: %v", err)
	}
}

// keeping is a store that keeps exactly what it is given.
type keeping struct{ held map[ir.NodeID][]byte }

func (k *keeping) Has(id ir.NodeID) bool { _, ok := k.held[id]; return ok }

func (k *keeping) Get(id ir.NodeID) ([]byte, error) {
	b, ok := k.held[id]
	if !ok {
		return nil, ErrNotFetched
	}

	return b, nil
}

func (k *keeping) Put(r io.Reader) (ir.NodeID, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return ir.NodeID{}, 0, err
	}

	id := ir.DigestOf(b)
	k.held[id] = b

	return id, int64(len(b)), nil
}

// Nothing beside it is the ordinary case and is not an error.
func TestAStoreWithNoNodesDirectory(t *testing.T) {
	t.Parallel()

	s := WithNodes(&Layers{Root: t.TempDir()}, t.TempDir())

	if s.Has(ir.DigestOf([]byte("never filed"))) {
		t.Error("claimed a node it does not have")
	}
}
