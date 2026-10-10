package overlay

import (
	"os"
	"path/filepath"
	"testing"
)

// The engine's own storage is not world-readable.
//
// The symlink farm is a directory this engine makes for itself - it holds short
// names for layers so the mount options fit in a page (E163) - and nothing
// outside the engine has any business in it. It was created 0755, which is the
// mode a *layer's* directories need and the wrong default for a private one
// (gosec G301).
//
// The distinction is the whole of the change. A directory whose mode is part of
// what a build produced must keep the mode the build gave it: tightening those
// would alter the image, and §3.3 lists a mode among what a layer records. A
// directory the engine invents for its own bookkeeping has no such claim on it.
func TestTheSymlinkFarmIsPrivate(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "l")
	target := t.TempDir()

	// Long enough to be shortened; `link` returns the target unchanged for a
	// name it cannot shorten, and would then make no directory at all.
	const id = "0123456789abcdef0123456789abcdef"

	at := link(dir, target, id)
	if at == target {
		t.Fatalf("no short name was made, so this asserts nothing about the farm")
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	if perm := fi.Mode().Perm(); perm&0o007 != 0 {
		t.Errorf("the farm is %o, which lets anyone on the machine read the"+
			" engine's layer names", perm)
	}
}
