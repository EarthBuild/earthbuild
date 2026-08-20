package guest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ensureFile makes sure a bind mount has a file to land on.
//
// A file source needs a file at the target: bind-mounting a file onto a
// directory fails, and onto nothing at all fails too. So the target is created
// when it is missing - and *only* when it is missing.
//
// The distinction is the point. Creating by opening is the obvious way to write
// this, and it opens whatever is already at that path. What is already there,
// once another step has prepared the same mount point, is the device that step
// bound: opening `/dev/tty` with no controlling terminal returns ENXIO, so a
// build with two concurrent steps failed on every machine without a terminal
// and on none with one (E52).
//
// Nothing here needs the file's contents, so nothing here opens it.
func ensureFile(target string, perm os.FileMode) error {
	_, err := os.Lstat(target)
	if err == nil {
		// Already a path. Whatever it is, a bind will replace what is visible
		// there, and this is not the place to have an opinion about it.
		return nil
	}

	// Inside the step's own filesystem, so this is a directory the build will
	// be judged on: a mount point's parent that a capture would record, and
	// 0750 would put a mode in the layer that nothing in the Earthfile asked
	// for (gosec G301).
	err = os.MkdirAll(filepath.Dir(target), 0o755) //nolint:gosec // a mode a build sees
	if err != nil {
		return fmt.Errorf("create the directory for %s: %w", target, err)
	}

	// O_EXCL, and an existing path is success.
	//
	// The Lstat above is check-then-act: another step preparing the same mount
	// point can bind its device between the two, and then this open lands on
	// *that* - ENXIO for a tty, and the same for a socket, which is what the
	// stress test uses because it needs no privileges. It reproduces on the
	// first iteration, so the window is wide rather than narrow.
	//
	// With O_EXCL there is no window: the call either creates the file or
	// refuses to touch what is there, and refusing is the answer this function
	// wants. Nothing here needs the file, only a path for a bind to land on.
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_RDONLY, perm) //nolint:gosec // see above
	if errors.Is(err, os.ErrExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}

	return f.Close()
}
