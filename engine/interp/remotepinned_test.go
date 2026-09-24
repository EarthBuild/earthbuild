package interp_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

const pinnedSHA = "51fe8fb974fd27cac120487c04948bd3295683c9"

// A fetched `LOCALLY` is allowed when the commit is named, and not otherwise.
//
// **Pinning changes what the refusal is defending against.** A branch or a tag
// is whatever the person with push access last made it, so `LOCALLY` behind one
// is a command chosen later by somebody else and run as you (green paper §5.3,
// E439). A full commit hash is not: the commands are fixed, and you can read
// them before you name them. The refusal stands for the first and has nothing
// to say about the second.
//
// The chain is what is checked, not the reference in front of you. A pinned
// repository that imports an unpinned one has moved the choice one hop away
// without removing it, so the pinned link buys nothing and the refusal holds.
func TestAFetchedLocallyNeedsThePinToBeReal(t *testing.T) {
	t.Parallel()

	t.Run("a commit hash is enough", func(t *testing.T) {
		t.Parallel()

		f := hostileRemote(t)

		_, err := interp.Build(versioned+
			"\nmain:\n    FROM github.com/org/repo:"+pinnedSHA+"+dangerous\n",
			testMain, interp.WithRemotes(f.fetch))
		if err != nil {
			t.Errorf("a LOCALLY behind a pinned commit was refused: %v"+
				"\n  the commands cannot change under a hash, which is the whole"+
				" of what the refusal defends against", err)
		}
	})

	t.Run("a branch is not", func(t *testing.T) {
		t.Parallel()

		f := hostileRemote(t)

		_, err := interp.Build(versioned+
			"\nmain:\n    FROM github.com/org/repo:main+dangerous\n",
			testMain, interp.WithRemotes(f.fetch))
		if err == nil {
			t.Fatal("a LOCALLY behind a branch was allowed" +
				"\n  a branch is whatever its author last pushed")
		}

		// The reader has to be told what to do about it, and that doing it is a
		// decision rather than a formality.
		for _, want := range []string{"pinned", "--unsafe-allow-unpinned-remote-locally"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal %q does not mention %q", err, want)
			}
		}
	})

	t.Run("an unsafe run may say so", func(t *testing.T) {
		t.Parallel()

		f := hostileRemote(t)

		_, err := interp.Build(versioned+
			"\nmain:\n    FROM github.com/org/repo:main+dangerous\n",
			testMain, interp.WithRemotes(f.fetch),
			interp.WithUnsafeUnpinnedRemoteLocally(true))
		if err != nil {
			t.Errorf("the escape hatch did not open: %v"+
				"\n  a caller who knows their context must be able to say so", err)
		}
	})
}
