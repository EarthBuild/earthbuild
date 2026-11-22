package guest

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/trace"
)

// The ordinary case is not lossy.
//
// The companion, because "declare lossy" is satisfiable by declaring
// everything lossy - and then the source is honest, useless, and
// indistinguishable from not having been written. The same guard
// TestAnOrdinaryCopyIsNotLossy is for the other observation source.
func TestAnOrdinaryTracedReadIsNotLossy(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	if err := os.WriteFile(filepath.Join(h.root, "w", "plain.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s.recordSightings(h, h.root, trace.Sightings{Paths: []string{"/w/plain.txt"}}, nil, nil)

	if s.observationOf(h).Incomplete {
		t.Error("a plain traced read declared itself lossy, so no step could ever" +
			" produce a usable observation")
	}
}

// A hardlink to a regular file is not lossy.
//
// A hardlink is not a separate object - it is a second name for one inode - so
// the digest taken at either name moves when the content does, and there is
// nothing unrecorded. Pinned because the fix above could over-broaden into
// "any link", which would cost hits for no safety at all.
func TestATracedReadOfAHardlinkIsNotLossy(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	real := filepath.Join(h.root, "w", "real.txt")
	if err := os.WriteFile(real, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Link(real, filepath.Join(h.root, "w", "hard")); err != nil {
		t.Skipf("hardlinks are not available here: %v", err)
	}

	s.recordSightings(h, h.root, trace.Sightings{Paths: []string{"/w/hard"}}, nil, nil)

	if s.observationOf(h).Incomplete {
		t.Error("a traced read of a hardlink declared itself lossy, though a" +
			" hardlink is the file: its digest moves when the content does")
	}
}

// A hardlink to a symlink is followed to the bottom, being a symlink.
//
// The case that prompted this work: `hard -> link -> real`. The hardlink is
// transparent - it and the symlink are one inode - and that inode is a link, so
// the chain is one hop and not two. Hardlinks never lengthen a chain; only
// symlinks do, which is why the resolver only ever asks whether the thing at a
// path is a link.
func TestATracedReadOfAHardlinkToASymlinkIsFollowed(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	real := filepath.Join(h.root, "w", "real.txt")
	if err := os.WriteFile(real, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(h.root, "w", "link")
	if err := os.Symlink("real.txt", link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	hard := filepath.Join(h.root, "w", "hard")
	if err := os.Link(link, hard); err != nil {
		t.Skipf("hardlinks are not available here: %v", err)
	}

	// `link(2)` does not follow symlinks on Linux and does on some BSDs, so
	// what was actually created is asked rather than assumed - a test that
	// hardlinked the *target* would pass for the wrong reason.
	fi, err := os.Lstat(hard)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Skip("this platform's link(2) followed the symlink, so there is no" +
			" hardlink-to-a-symlink here to test")
	}

	at, lossy := readsOfSighting(t, s, h, "/w/hard")

	if lossy {
		t.Error("a hardlink to a symlink was given up on rather than followed")
	}

	if !at["/w/real.txt"] {
		t.Errorf("the chain did not reach the file: recorded %v", at)
	}
}
