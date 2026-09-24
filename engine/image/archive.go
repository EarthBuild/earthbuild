package image

import (
	"fmt"
	"os"
)

// WriteArchive writes an image as an OCI layout and then as a tar beside it.
//
// The form `docker load` takes. Two artefacts because the tar is what is loaded
// and the layout is what produced it, and keeping the layout costs a directory
// and makes the archive inspectable when a load goes wrong.
//
// **One implementation, called from both sides of the sandbox boundary.** The
// host packs an image where it can open the store; a guest whose store is a
// disk packs it there. The image a `WITH DOCKER --load` gets must not depend on
// which happened, and the surest way to arrange that is for there to be one
// piece of code that could have (E558).
func WriteArchive(dir string, spec Spec) error {
	err := os.RemoveAll(dir)
	if err != nil {
		return fmt.Errorf("clear the previous %s: %w", spec.Ref, err)
	}

	err = WriteLayout(dir, spec)
	if err != nil {
		return fmt.Errorf("write %s: %w", spec.Ref, err)
	}

	f, err := os.Create(dir + ".tar") //nolint:gosec // a path this engine derived
	if err != nil {
		return fmt.Errorf("create the archive for %s: %w", spec.Ref, err)
	}

	_, _, err = Pack(dir, f)
	if err != nil {
		_ = f.Close()

		return fmt.Errorf("archive %s: %w", spec.Ref, err)
	}

	err = f.Close()
	if err != nil {
		return fmt.Errorf("finish the archive for %s: %w", spec.Ref, err)
	}

	return nil
}
