package guest

import (
	"fmt"
	"os"
	"path/filepath"
)

// The OCI convention for recording a deletion in a layer that is a plain
// directory rather than an overlay upper.
//
// A tar cannot carry a character device without privilege, so every registry in
// the world already spells a whiteout this way; `image/whiteout.go` has read
// them since the beginning. This engine now *writes* them for the same reason
// it reads them: the layer store is a host directory shared into the sandbox,
// and a share whose host filesystem has no device nodes cannot hold one (E88).
const (
	whPrefix = ".wh."
	whOpaque = ".wh..wh..opq"
)

// writeWhiteout records that `target` was deleted, portably.
//
// An empty regular file named `.wh.<name>` beside where the entry would be,
// which any filesystem can hold. The materialiser turns it back into the
// character device overlayfs wants, on storage inside the VM where mknod works
// (see engine/mat/overlay).
//
// The alternative this replaces was to refuse the build, which was honest and
// made a macOS host unable to run any Earthfile containing `rm`.
func writeWhiteout(target string) error {
	marker := filepath.Join(filepath.Dir(target), whPrefix+filepath.Base(target))

	err := os.WriteFile(marker, nil, 0o600)
	if err != nil {
		return fmt.Errorf("record the deletion of %s: %w", filepath.Base(target), err)
	}

	return nil
}

// writeOpaque records that a directory replaces the one below it.
//
// The other half of how a deletion is spelled: removing a whole directory marks
// its replacement opaque, which overlayfs stores as an xattr and a tar stores as
// a `.wh..wh..opq` entry inside it.
func writeOpaque(dir string) error {
	err := os.WriteFile(filepath.Join(dir, whOpaque), nil, 0o600)
	if err != nil {
		return fmt.Errorf("record that %s is opaque: %w", filepath.Base(dir), err)
	}

	return nil
}
