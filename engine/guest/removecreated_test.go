package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// A mount point this engine made is taken away, and one the image had is not.
//
// **A mount is a hole**: what the step wrote into it is not part of what the
// step produced, and a directory made only so there was something to bind onto
// belongs to this engine rather than to the step. Leaving one behind put an
// empty `/cache` in the image where the reference engine puts nothing (E33).
//
// Removed deepest first, because a directory containing another cannot go
// first, and only when empty - `os.Remove` refusing a non-empty directory is
// the guard rather than an inconvenience, since a mount point the image already
// had keeps whatever the image put in it.
//
// Six of these cost 1.37ms each against 0.055ms for the unmount above them,
// which is a round trip to the host rather than work, so they are removed a
// depth at a time rather than one after another. Two directories at the same
// depth are never nested, which is what makes that safe.
func TestOnlyTheMountPointsThisEngineMadeAreRemoved(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Nested, and handed over parent-first so the ordering cannot come from the
	// order they arrive in.
	outer := filepath.Join(root, "cache")
	inner := filepath.Join(outer, "inner")
	// The image's own, with something in it.
	kept := filepath.Join(root, "etc")
	keptFile := filepath.Join(kept, "hosts")
	// A sibling of the nested pair, to have two at one depth.
	sibling := filepath.Join(root, "scratch")

	for _, d := range []string{inner, kept, sibling} {
		err := os.MkdirAll(d, 0o755)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := os.WriteFile(keptFile, []byte("127.0.0.1 localhost\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	removeCreated([]string{outer, kept, sibling, inner})

	for _, gone := range []string{inner, outer, sibling} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived, and it was this engine's mount point"+
				"\n  a directory made only to bind onto is not the step's, and"+
				" leaving it behind puts it in the step's layer (E33)",
				filepath.Base(gone))
		}
	}

	if _, err := os.Stat(keptFile); err != nil {
		t.Errorf("%s was removed, and the image put it there: %v"+
			"\n  only an empty mount point is this engine's to take away",
			filepath.Base(kept), err)
	}
}
