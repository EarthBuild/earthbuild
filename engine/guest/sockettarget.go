package guest

import (
	"fmt"
	"os"
	"path/filepath"
)

// socketTargetIn is where a daemon's socket has to be bound so that the step
// finds it at the path its client looks in.
//
// **The directory is usually a symlink.** `/var/run -> ../run` is in every
// Alpine-derived image, which includes the official docker client images, so a
// bind placed at `<root>/var/run/docker.sock` unresolved is placed *through* the
// link - `<root>/var/run` resolves on the guest to `<root>/../run`, outside the
// step altogether. The step then finds nothing where it looks, and the engine
// has written somewhere it had no business writing (E397).
//
// Resolved the way the step would resolve it, with the helper written for `COPY`
// and for the same reason: the link's text is read against the step's root, and a
// relative target that climbs out is clamped, which is what the kernel does above
// a chroot. An image is an input and §5.3 does not trust one - a
// `/var/run -> ../../../etc` in a base image must not choose where this engine
// binds a live docker socket.
//
// The directory is made when it does not exist: a scratch image has nothing at
// all, and the socket still has to appear somewhere.
func socketTargetIn(root, at string) (string, error) {
	dir := filepath.Dir(at)

	_, err := os.Lstat(dir)
	if err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a mode a build sees
			return "", fmt.Errorf("make the directory the step's client looks in: %w", err)
		}

		return at, nil
	}

	real, err := resolveLast(root, dir)
	if err != nil {
		return "", fmt.Errorf("find where %s leads inside the step: %w", dir, err)
	}

	if err := os.MkdirAll(real, 0o755); err != nil { //nolint:gosec // a mode a build sees
		return "", fmt.Errorf("make %s for the daemon's socket: %w", real, err)
	}

	return filepath.Join(real, filepath.Base(at)), nil
}
