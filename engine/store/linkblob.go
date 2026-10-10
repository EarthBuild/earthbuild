package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/engine/ir"
)

// LinkBlob makes a file inside a layer fetchable under its own digest.
//
// **A link, because this store is insert-only.** A committed layer is never
// rewritten (I9), so a node and the layer file it shares an inode with cannot
// diverge - which is what makes sharing one safe rather than clever. Both live
// under the store root and so on one filesystem, so the link costs nothing and
// no bytes move. Collection is unharmed in either direction: removing a layer
// leaves a node that still has a reference, and sweeping a node leaves the
// layer's file alone.
//
// Checked against the name it is filed under, which is the store's one rule: a
// link that put a file under a digest its contents do not produce would be a
// store that lies on every later read, and reads verify precisely because
// writes might not have.
//
// Falls back to copying where a link is impossible - a layer on another device,
// a filesystem without them. The result is the same blob under the same name,
// more slowly.
func (d DirStore) LinkBlob(layerID ir.NodeID, rel string, id ir.NodeID) error {
	at := NodePath(string(d), id)

	// Named by its bytes, so what is there is what would go.
	if _, err := os.Stat(at); err == nil {
		return nil
	}

	from := filepath.Join(LayerStore(string(d)).Path(layerID), filepath.FromSlash(rel))

	if err := checkNamed(from, id); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
		return fmt.Errorf("make somewhere for %s: %w", id, err)
	}

	err := os.Link(from, at)
	if err == nil {
		return nil
	}

	// **Present already is not a failure**, and two actions producing one file
	// is ordinary: they raced, and both were right.
	if errors.Is(err, os.ErrExist) {
		return nil
	}

	return copyBlob(from, at, id)
}

// checkNamed refuses a file whose contents do not produce the name it is being
// filed under.
func checkNamed(from string, id ir.NodeID) error {
	f, err := os.Open(from) //nolint:gosec // a path this engine wrote
	if err != nil {
		return fmt.Errorf("read %s to file it under %s: %w", from, id, err)
	}

	defer func() { _ = f.Close() }()

	h := ir.NewStreamHasher()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", from, err)
	}

	if got := h.Sum(); got != id {
		return fmt.Errorf(
			"%s holds %s and is being filed under %s"+
				"\n  a blob is named by its contents, and one filed under any other name"+
				"\n  is a store that answers the wrong bytes for ever after",
			from, got, id)
	}

	return nil
}

// copyBlob is LinkBlob's answer where a link cannot be made.
func copyBlob(from, at string, id ir.NodeID) error {
	b, err := os.ReadFile(from) //nolint:gosec // a path this engine wrote
	if err != nil {
		return fmt.Errorf("read %s to file it under %s: %w", from, id, err)
	}

	return writeNode(at, b)
}
