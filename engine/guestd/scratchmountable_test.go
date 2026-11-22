package guestd

import (
	"os"
	"regexp"
	"testing"
)

// The materialiser routes its scratch through overlay.Mountable.
//
// overlayfs will not stack on overlayfs, so a guest whose scratch sits on the
// step's own root cannot materialise its first base: it fails with `invalid
// argument`, which names nothing about the cause. That is every containerised CI
// runner, this repository's own included.
//
// `Mountable` has known the way out since before anything used it - try where
// the caller asked, then a tmpfs, which overlayfs will stack on. **It was reached
// only from tests.** The escape the engine wrote for itself was the one thing
// production never took, which is what E634 is named for.
//
// A source guard, because the alternative is a test that must mount an overlay
// to be meaningful, and because the property is about which function the
// production path calls rather than about a value it returns. The mutant that
// replaces the call with the raw scratch path survived the whole suite; this is
// what notices.
func TestTheScratchIsRoutedThroughMountable(t *testing.T) {
	t.Parallel()

	// Read as text: the file is linux-only and this test is not, which is the
	// point - the guard should fail on a mac too, where nobody would otherwise
	// run the code that breaks.
	b, err := os.ReadFile("mat_linux.go")
	if err != nil {
		t.Fatal(err)
	}

	src := string(b)

	// This file names the pattern in order to look for it.
	call := regexp.MustCompile(`overlay\.Mountable\(`)
	if !call.MatchString(src) {
		t.Error("mat_linux.go does not route its scratch through" +
			" overlay.Mountable, so a guest whose scratch is on an overlay" +
			" cannot materialise its first base - which is every containerised" +
			" runner, and the failure names nothing about the cause")
	}

	// And the result is what the materialiser is built on, not a value taken
	// and dropped. `at` is the relocated path; using `scratch` afterwards would
	// take the answer and ignore it.
	uses := regexp.MustCompile(`newStackedAt\(|Materialiser\([^)]*\bat\b|\bat\b[^\n]*scratch`)
	if !uses.MatchString(src) {
		t.Error("the relocated path does not reach the materialiser: the" +
			" escape is computed and then not taken")
	}
}
