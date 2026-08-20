package image

import (
	"archive/tar"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	whPrefix = ".wh."
	whOpaque = ".wh..wh..opq"
)

// whiteout applies a deletion marker, reporting whether the entry was one.
//
// An image's layers are unpacked into one directory here, so a deletion is a
// deletion. The overlayfs form - a character device 0:0, or a
// `trusted.overlay.opaque` attribute - describes a layer that stays *separate*
// and is stacked later; in a tree that has already been flattened it is at best
// meaningless and at worst a stray device file in the image root.
//
// Writing it also needed CAP_MKNOD and CAP_SYS_ADMIN, which is why this worked
// only on Linux and as root: `clojure:temurin-8-lein` could not be pulled at
// all on a developer's machine, and the diagnosis said overlayfs to somebody who
// had not asked for one.
//
// Build layers are a different matter and still stack: `engine/mat/overlay` is
// where that model lives, and nothing here touches it.
func whiteout(h *tar.Header, target string) (bool, error) {
	base := filepath.Base(h.Name)

	switch {
	case base == whOpaque:
		// Everything a lower layer put in this directory is hidden. Flattened,
		// that means removing what is there and keeping what this layer adds -
		// which works because entries arrive in order and this marker comes
		// before them.
		dir := filepath.Dir(target)

		entries, err := os.ReadDir(dir)
		if err != nil {
			// Nothing there to hide.
			return true, nil //nolint:nilerr // see above
		}

		for _, e := range entries {
			err := RemoveAll(filepath.Join(dir, e.Name()))
			if err != nil {
				return true, fmt.Errorf("apply the opaque marker in %q: %w", h.Name, err)
			}
		}

		return true, nil

	case strings.HasPrefix(base, whPrefix):
		deleted := filepath.Join(filepath.Dir(target), strings.TrimPrefix(base, whPrefix))

		// Absent is not an error: layers are built independently, and an image
		// may delete a path that a different base once had. Refusing would fail
		// a pull over an image that is merely cautious.
		err := RemoveAll(deleted)
		if err != nil {
			return true, fmt.Errorf("apply the whiteout %q: %w", h.Name, err)
		}

		return true, nil
	}

	return false, nil
}
