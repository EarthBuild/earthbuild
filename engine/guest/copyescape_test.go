package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// Copying a tree cannot write through a symlink at the destination.
//
// The sibling of the same fault in the image cache, and here the confinement it
// breaks is A3's. A step writes into its own layer; a copy into that layer that
// followed a planted symlink would write into whatever the link names - the
// shared store, another layer, the guest's own root - and the step's result
// would stop being bounded by the step.
//
// The link does not have to be planted during the copy. A step earlier in the
// same build can leave it, because its filesystem is the one being copied into.
func TestCopyingATreeCannotWriteThroughASymlink(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := t.TempDir()
	outside := t.TempDir()

	err := os.MkdirAll(filepath.Join(src, "opt", "app"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(src, "opt", "app", "run"), []byte("payload"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(outside, filepath.Join(dst, "opt"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	_ = copyTree(src, dst, copyOpts{})

	_, err = os.Stat(filepath.Join(outside, "app", "run"))
	if err == nil {
		t.Error("the copy wrote through a symlink, outside the destination")
	}
}
