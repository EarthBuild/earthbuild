package store

import (
	"os"
	"path/filepath"
	"testing"
)

// Placing an image in the layer store cannot write outside it.
//
// The store is shared: the host writes layers into it, and the guest - which is
// running somebody's `RUN` command - writes into it too, over virtiofs. That is
// the design and it is fine, because the guest is confined to the store.
//
// It stops being fine if the guest can make the *host* write somewhere else.
// `linkTree` walks a cached image and creates directories and links under a
// destination in the store; every one of those calls follows symlinks. A step
// that plants a symlink where the host is about to write turns "the guest may
// write anywhere in the store" into "the guest may write anywhere the build
// tool can" - which on a developer's machine is everything they own.
//
// Not a hypothetical ordering race: the symlink can be sitting there before the
// build starts, left by any earlier step of any earlier build.
func TestPlacingAnImageCannotWriteOutsideTheStore(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	store := t.TempDir()
	outside := t.TempDir()

	// An image with one file in a directory.
	err := os.MkdirAll(filepath.Join(src, "usr", "bin"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(src, "usr", "bin", "tool"), []byte("payload"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// What a step left behind: the destination's `usr` is a link out of the
	// store. Nothing about the image is unusual; the trap is in the store.
	dst := filepath.Join(store, "layers", "sha256-x")

	err = os.MkdirAll(dst, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(outside, filepath.Join(dst, "usr"))
	if err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	// The error is not the point - refusing is one right answer and so is
	// replacing the link - so only the escape is asserted.
	_ = LinkTree(src, dst)

	_, err = os.Stat(filepath.Join(outside, "bin", "tool"))
	if err == nil {
		t.Error("the image was written through a symlink, outside the store")
	}
}
