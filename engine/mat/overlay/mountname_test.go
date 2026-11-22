package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two mount directories are never the same directory.
//
// The property, stated without reference to how the name is produced. It used
// to be `h<pid>-<counter>`, and the test that guarded it asserted the
// mechanism:
//
//	if runID != os.Getpid() { … }
//	// Asserted because it is the whole mechanism: a value that happened to be
//	// constant across processes would satisfy every test above and fix nothing.
//
// Which was right, and describes exactly what happened. The guest gained a PID
// namespace, `os.Getpid()` became 1 for every guest there has ever been, and the
// value that "happened to be constant across processes" was the one the test
// insisted on (E140).
//
// **A test that asserts the mechanism cannot notice the mechanism's premise
// failing.** So this asserts the property, and the property is asked of the
// filesystem: `MkdirTemp` is the only party that can promise a name nobody has.
func TestTwoMountDirectoriesAreNeverTheSame(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	seen := map[string]bool{}

	// Enough to catch a scheme that repeats after a few, and cheap: these are
	// directories, not mounts.
	for range 64 {
		//nolint:usetesting // under root, with the engine's prefix: both are what is under test
		d, err := os.MkdirTemp(root, mountPrefix)
		if err != nil {
			t.Fatal(err)
		}

		if seen[d] {
			t.Fatalf("%s was handed out twice", d)
		}

		seen[d] = true

		if !strings.HasPrefix(filepath.Base(d), mountPrefix) {
			t.Errorf("%s does not carry the prefix, so a reader cannot tell what made it", d)
		}
	}
}

// A dead build's directory is not one a new build asks for.
//
// The case the old scheme existed for, and the one the new one gets for free: a
// killed guest leaves its mounts behind, and a name nobody has is a name no
// corpse is holding. Simulated by leaving a directory where a mount would be
// and asking for another.
func TestANewMountAvoidsWhatIsLeftBehind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	//nolint:usetesting // under root, with the engine's prefix: both are what is under test
	dead, err := os.MkdirTemp(root, mountPrefix)
	if err != nil {
		t.Fatal(err)
	}

	//nolint:usetesting // under root, with the engine's prefix: both are what is under test
	live, err := os.MkdirTemp(root, mountPrefix)
	if err != nil {
		t.Fatal(err)
	}

	if dead == live {
		t.Errorf("a new mount reused a directory the last build left: %s", dead)
	}
}
