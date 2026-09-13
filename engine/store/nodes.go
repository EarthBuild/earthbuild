package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

// NodePath is where one directory node of a tree lives.
//
// Beside the layers rather than under them, because a node belongs to no layer:
// the whole point of naming a subtree by its contents is that every base holding
// that subtree holds the same node, so filing it under the base that happened to
// be written first would hide it from the rest.
func NodePath(layerDir string, id ir.NodeID) string {
	return filepath.Join(layerDir, "nodes", id.String())
}

// NoteNodes writes a tree's directories down, each under its own name.
//
// Idempotent and shared: a node already present is already the same bytes, since
// its name is ℋ over them. A second tree sharing a subtree therefore writes
// nothing for it, which is what makes the second transfer small.
func (d DirStore) NoteNodes(t layer.Tree) error {
	at := filepath.Join(string(d), "nodes")
	if err := os.MkdirAll(at, 0o750); err != nil {
		return fmt.Errorf("make %s: %w", at, err)
	}

	for id, b := range t.Nodes() {
		p := NodePath(string(d), id)

		if _, err := os.Stat(p); err == nil {
			continue // named by its bytes, so what is there is what would go
		}

		if err := writeNode(p, b); err != nil {
			return err
		}
	}

	return nil
}

// writeNode puts one node down under a temporary name and renames it.
//
// **A half-written node under its final name is a lie about its own digest**,
// and the whole contract here is that the bytes at a name hash to it - so a
// reader finding a truncated file would reject a node the store does in fact
// have, and keep rejecting it.
func writeNode(at string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(at), ".node-*")
	if err != nil {
		return fmt.Errorf("stage node beside %s: %w", at, err)
	}

	_, err = tmp.Write(b)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write node %s: %w", at, err)
	}

	if err := os.Rename(tmp.Name(), at); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("place node %s: %w", at, err)
	}

	return nil
}

// MissingNodes is which of these nodes the store does not hold.
//
// **The missing set, not the held one**, which is the other way round from
// StoreHas. A build asks about a handful of layers and wants to know which it
// can use; a peer receiving a tree holds nearly every node of it already and
// wants to know the few to ask for. The reply is the one the caller acts on, and
// on a base of tens of thousands of entries it is the difference between a list
// of two and a list of seven hundred.
//
// Order follows the request, so a caller can pair a reply with what it asked.
func (d DirStore) MissingNodes(ids []ir.NodeID) []ir.NodeID {
	var out []ir.NodeID

	for _, id := range ids {
		if _, err := os.Stat(NodePath(string(d), id)); err != nil {
			out = append(out, id)
		}
	}

	return out
}

// Node is one directory's bytes, checked against the name they were asked for.
//
// **Checked here rather than trusted**, because a node is the thing a peer sends
// and a store that files whatever arrives under whatever name it was given is
// not content-addressed. The same check catches a node corrupted on disk, which
// is the case this can actually reach today.
func (d DirStore) Node(id ir.NodeID) ([]byte, error) {
	p := NodePath(string(d), id)

	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read node %s: %w", id, err)
	}

	if got := ir.DigestOf(b); got != id {
		return nil, fmt.Errorf(
			"node %s holds bytes naming %s\n  the store's copy is not what it is"+
				" filed as, so anything built from it is not the tree that was asked"+
				" for - remove %s and it will be fetched again", id, got, p)
	}

	return b, nil
}
