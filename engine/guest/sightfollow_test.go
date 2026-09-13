package guest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/trace"
)

// readsOfSighting records one traced path and returns what was observed.
func readsOfSighting(t *testing.T, s *Server, h fixedHandle, p string) (map[string]bool, bool) {
	t.Helper()

	s.recordSightings(h, h.root, trace.Sightings{Paths: []string{p}, Opened: []string{p}}, nil, nil)

	obs := s.observationOf(h)
	at := make(map[string]bool, len(obs.Reads))

	for k := range obs.Reads {
		at[k] = true
	}

	return at, obs.Incomplete
}

// A symlink is bottomed out, not given up on.
//
// Recording only the link keyed the step on a value that does not move when the
// file behind it changes; declaring the observation lossy was correct and cost
// the hit. Following it keeps both: the link's own digest catches a repoint, and
// the target's catches an edit.
func TestATracedReadFollowsASymlinkToItsTarget(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	if err := os.WriteFile(filepath.Join(h.root, "w", "real.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("real.txt", filepath.Join(h.root, "w", "link")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	at, lossy := readsOfSighting(t, s, h, "/w/link")

	if lossy {
		t.Error("still lossy: the link was not followed")
	}

	if !at["/w/link"] {
		t.Error("the link itself was not recorded, so a repoint would not be caught")
	}

	if !at["/w/real.txt"] {
		t.Errorf("the target was not recorded, so an edit behind the link would"+
			"\n  not be caught - recorded %v", at)
	}
}

// A chain bottoms out, however long, and hardlinks along it cost nothing.
//
// A hardlink is a second name for an inode rather than a hop, so only symlinks
// lengthen a chain. This is the `symlink -> hardlink -> symlink -> ...` shape
// with the hardlinks removed, because they were never there as steps.
func TestATracedReadFollowsAChainOfSymlinks(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	w := filepath.Join(h.root, "w")
	if err := os.WriteFile(filepath.Join(w, "bottom.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prev := "bottom.txt"
	for i := range 5 {
		name := filepath.Join(w, "hop"+string(rune('a'+i)))
		if err := os.Symlink(prev, name); err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}

		prev = filepath.Base(name)
	}

	at, lossy := readsOfSighting(t, s, h, "/w/hope")

	if lossy {
		t.Error("a five-hop chain was given up on rather than bottomed out")
	}

	if !at["/w/bottom.txt"] {
		t.Errorf("the chain did not reach the file: recorded %v", at)
	}

	// **Every hop, not just the ends.** A link in the middle of a chain can be
	// repointed while the one above it is untouched: `a` still says `b`, so
	// `a`'s own digest has not moved, and only `b`'s records that it now names
	// something else. Recording the bottom and the top would miss exactly that.
	for i := range 5 {
		hop := "/w/hop" + string(rune('a'+i))
		if !at[hop] {
			t.Errorf("%s was not recorded, so repointing it would not be caught"+
				"\n  recorded %v", hop, at)
		}
	}
}

// A symlink out of the mount is refused, not followed.
//
// **The one that must never become a read.** Following it would digest a file
// the base does not hold, under a path that means something else next time -
// and would have this process hash whatever the step could point at.
func TestATracedReadRefusesASymlinkOutOfTheMount(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("not yours\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(h.root, "w", "escape")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	at, lossy := readsOfSighting(t, s, h, "/w/escape")

	if !lossy {
		t.Error("a symlink out of the mount was not declared lossy")
	}

	for p := range at {
		if filepath.IsAbs(p) && !filepath.HasPrefix(p, "/w") {
			t.Errorf("recorded %q, which is outside the mount", p)
		}
	}

	if at[outside] {
		t.Error("the escaping target was recorded as a read of this base")
	}
}

// A loop is given up on rather than followed for ever.
func TestATracedReadRefusesASymlinkLoop(t *testing.T) {
	t.Parallel()

	s, h := copyFixture(t)

	w := filepath.Join(h.root, "w")
	if err := os.Symlink("loopb", filepath.Join(w, "loopa")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	if err := os.Symlink("loopa", filepath.Join(w, "loopb")); err != nil {
		t.Fatal(err)
	}

	done := make(chan bool, 1)

	go func() {
		_, lossy := readsOfSighting(t, s, h, "/w/loopa")
		done <- lossy
	}()

	select {
	case lossy := <-done:
		if !lossy {
			t.Error("a symlink loop did not declare the observation lossy")
		}
	case <-t.Context().Done():
		t.Fatal("a symlink loop was followed without bound")
	}
}
