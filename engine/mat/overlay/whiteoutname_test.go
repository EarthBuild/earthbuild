package overlay

import (
	"strings"
	"testing"
)

// A whiteout marker names a sibling, and a layer cannot make it name anything else.
//
// **`.wh...` used to strip to `..`.** The name comes out of a layer archive,
// `strings.TrimPrefix(".wh...", ".wh.")` is `".."`, and `filepath.Join` then
// resolves that to the parent of the directory being translated - which the
// engine went on to `Mknod`, outside the destination (gosec G703, E630).
//
// Nothing escaped, because the parent exists and `Mknod` refuses an existing
// path. The guard was the filesystem's rather than the engine's, and a
// destination whose parent did not exist would have had a device node written
// beside it.
func TestAWhiteoutCannotNameSomethingOutsideItsDirectory(t *testing.T) {
	t.Parallel()

	for _, marker := range []string{
		".wh...",   // strips to ".."
		".wh..",    // strips to "."
		".wh.",     // strips to nothing
		".wh./etc", // a path rather than a name
		".wh.a/b",
	} {
		got, err := whiteoutTarget(marker)
		if err == nil {
			t.Errorf("%q was accepted as a whiteout for %q", marker, got)
		}
	}
}

// An ordinary marker still works, or the guard deletes the feature.
func TestAnOrdinaryWhiteoutIsAccepted(t *testing.T) {
	t.Parallel()

	for marker, want := range map[string]string{
		".wh.foo":            "foo",
		".wh..bashrc":        ".bashrc",
		".wh.a..b":           "a..b",
		".wh.libstdc++.so.6": "libstdc++.so.6",
		".wh...hidden":       "..hidden",
	} {
		got, err := whiteoutTarget(marker)
		if err != nil {
			t.Errorf("%q was refused: %v", marker, err)

			continue
		}

		if got != want {
			t.Errorf("whiteoutTarget(%q) = %q, want %q", marker, got, want)
		}
	}
}

// Something that is not a marker at all is refused rather than mangled.
func TestANonMarkerIsRefused(t *testing.T) {
	t.Parallel()

	_, err := whiteoutTarget("ordinary.txt")
	if err == nil {
		t.Error("a file that is not a whiteout was read as one")
	}

	if err != nil && !strings.Contains(err.Error(), "ordinary.txt") {
		t.Errorf("the refusal does not name the entry: %v", err)
	}
}
