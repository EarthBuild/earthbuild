package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The default store is readable by its owner alone.
//
// It holds build contexts and layers - source code, and whatever a build
// fetched with credentials. `0750` was meant as private, and on macOS it is
// not: every local user is in group `staff`, which can enter a home directory,
// so the group bits opened the store to all of them.
//
// A store that already exists with the old mode is tightened. An explicit
// EARTH_CACHE_DIR is left alone: a shared cache is a choice somebody made.
func TestTheDefaultStoreIsPrivate(t *testing.T) {
	home := t.TempDir()

	t.Setenv("HOME", home)
	t.Setenv(envCacheDir, "")
	t.Setenv("XDG_CACHE_HOME", "")

	old := filepath.Join(home, ".cache", "earthbuild")

	err := os.MkdirAll(old, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Chmod(old, 0o750) //nolint:gosec // the mode being tightened
	if err != nil {
		t.Fatal(err)
	}

	dir, err := storeDir()
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	if mode := fi.Mode().Perm(); mode != 0o700 {
		t.Errorf("the store at %s is %04o, want 0700", dir, mode)
	}
}
