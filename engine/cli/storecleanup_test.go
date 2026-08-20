//go:build darwin

package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

// A test's store is deleted when the test ends, even when a layer in it denies
// writing.
//
// `os.RemoveAll` cannot delete a file inside a directory with no write bit -
// removing an entry needs permission on the directory holding it, not on the
// entry - and real images ship such directories: `maven:3.8.5-openjdk-17` has
// one, and unpacking it into a store leaves a tree `t.TempDir` cannot clear.
// The corpus build test found this by failing its own cleanup after building
// everything it was asked to.
//
// The store is where this bites, because a store holds unpacked layers with
// their modes intact - which is not incidental, it is what makes the step's
// filesystem right. So the tree cannot be relaxed; the removal has to cope.
func TestAStoreIsRemovedEvenWhenALayerDeniesWriting(t *testing.T) {
	t.Parallel()

	var dir string

	// A subtest so its cleanups have run by the time the assertion below does.
	t.Run("store", func(t *testing.T) {
		t.Parallel()

		dir = storeDir(t)

		locked := filepath.Join(dir, "layers", "sha256-x", "root", "usr", "bin")

		err := os.MkdirAll(locked, 0o755)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(locked, "mvn"), []byte("#!/bin/sh\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		// Read and traverse, but not write: the mode maven ships, applied last
		// so the file above could be written first.
		err = os.Chmod(locked, 0o555)
		if err != nil {
			t.Fatal(err)
		}
	})

	_, err := os.Stat(dir)
	if err == nil {
		t.Error("the store outlived the test that owned it")
	}
}
