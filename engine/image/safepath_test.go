package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every way a layer entry can try to leave its root is refused.
//
// **CodeQL reports `unpack.go` as "Arbitrary file access during archive
// extraction (Zip Slip)"**, and it is a false positive - but E625 was a guard
// that did not guard, so the claim is worth holding down rather than asserting.
// The sanitiser is `safePath`, called on every entry before anything touches the
// filesystem; CodeQL does not recognise it because the check is a function away
// from the use rather than inline at it.
//
// This is the table of vectors it refuses. A dismissal of that alert rests on
// this test, so the two belong together.
func TestNoLayerEntryCanEscapeItsRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, name := range []string{
		"..",
		"../etc/passwd",
		"../../../../etc/passwd",
		"a/../../etc/passwd",
		"a/b/../../../etc/passwd",
		"./../etc/passwd",
		"/etc/passwd",
		"//etc/passwd",
		"/",
		"",
	} {
		got, err := safePath(root, name)
		if err != nil {
			continue
		}

		// Anything accepted must be inside the root, which is the property the
		// refusals exist to protect. An accepted name that lands outside is the
		// Zip Slip itself.
		if got != root && !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Errorf("safePath(%q) = %q, which is outside %q", name, got, root)
		}
	}
}

// An ordinary entry still resolves, or the guard is a refusal of everything.
func TestAnOrdinaryLayerEntryIsAccepted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, name := range []string{".", "./", "usr/lib/libc.so", "./usr/bin/env", "a/b/c"} {
		got, err := safePath(root, name)
		if err != nil {
			t.Errorf("safePath(%q) refused an ordinary entry: %v", name, err)

			continue
		}

		if got != root && !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Errorf("safePath(%q) = %q, outside the root", name, got)
		}
	}
}

// And a symlink already in the tree cannot be written through, which is the case
// the `..` checks alone would miss: the name is innocent and the parent is not.
func TestAnEntryCannotBeWrittenThroughAPlantedSymlink(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()

	err := os.Symlink(outside, filepath.Join(root, "escape"))
	if err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	_, err = safePath(root, "escape/passwd")
	if err == nil {
		t.Error("an entry wrote through a symlink pointing out of the layer")
	}

	if err != nil && !strings.Contains(err.Error(), "symlink") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}
