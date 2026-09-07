//go:build linux

package overlay

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// tmpfs makes a filesystem overlayfs will stack on, and hands back how to
// remove it.
//
// The last resort in Mountable, and the engine's own advice: `mountHint` has
// been telling people to "mount a volume or a tmpfs" since before anything did
// it. A container's root is overlayfs and overlayfs will not stack on itself,
// so without this the Linux materialiser's conformance suite skips wherever it
// would be most useful - which is every CI run (E69).
//
// Needs CAP_SYS_ADMIN, which the caller has already established by being able
// to attempt an overlay mount at all.
func tmpfs() (string, func(), error) {
	dir, err := os.MkdirTemp("", "earth-tmpfs-*")
	if err != nil {
		return "", nil, fmt.Errorf("make a mount point: %w", err)
	}

	err = unix.Mount("tmpfs", dir, "tmpfs", 0, "")
	if err != nil {
		_ = os.RemoveAll(dir)

		return "", nil, fmt.Errorf("mount a tmpfs at %s: %w", dir, err)
	}

	return dir, func() {
		_ = unix.Unmount(dir, 0)
		_ = os.RemoveAll(dir)
	}, nil
}
