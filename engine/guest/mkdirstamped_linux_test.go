package guest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
)

// A directory the copy invents must carry the same time on every machine and in
// every build, or the layer holding it has a different identity each time and
// every step above it re-keys (E576).
func TestAnInventedDirectoryDoesNotCarryTheWallClock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	target := filepath.Join(root, "earthly", "build", "out")
	err := mkdirAllStamped(target, 0o755, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		filepath.Join(root, "earthly"),
		filepath.Join(root, "earthly", "build"),
		target,
	} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}

		if !fi.ModTime().Equal(fstime.Invented) {
			t.Errorf("%s carries %v, want the invented time %v",
				p, fi.ModTime(), fstime.Invented)
		}
	}
}

// Twice, from two different moments, is the property that matters: the same
// paths, the same times.
func TestTwoBuildsInventTheSameTimes(t *testing.T) {
	t.Parallel()

	first, second := t.TempDir(), t.TempDir()

	err := mkdirAllStamped(filepath.Join(first, "a", "b"), 0o755, nil)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)

	err = mkdirAllStamped(filepath.Join(second, "a", "b"), 0o755, nil)
	if err != nil {
		t.Fatal(err)
	}

	one, err := os.Stat(filepath.Join(first, "a"))
	if err != nil {
		t.Fatal(err)
	}

	two, err := os.Stat(filepath.Join(second, "a"))
	if err != nil {
		t.Fatal(err)
	}

	if !one.ModTime().Equal(two.ModTime()) {
		t.Errorf("two builds invented %v and %v", one.ModTime(), two.ModTime())
	}
}

// A directory that was already there is left alone: this stamps what it
// invented and nothing else.
//
// **Its time still moves, and not by our hand.** Adding an entry to a directory
// updates that directory's mtime - the filesystem does it, no call appears in
// any diff - so an ancestor that gains a child carries the wall clock whoever
// wrote it. That is the residue this fix does not reach, and the reason it is
// checked here rather than discovered later: what the assertion pins is that the
// invented time is *not* applied to somebody else's directory, not that the
// directory is unchanged, which would be a claim about the kernel.
func TestAnExistingDirectoryIsNotRestampedByUs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	existing := filepath.Join(root, "earthly")
	err := os.MkdirAll(existing, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	when := time.Unix(1000000, 0)
	err = os.Chtimes(existing, when, when)
	if err != nil {
		t.Fatal(err)
	}

	err = mkdirAllStamped(filepath.Join(existing, "made"), 0o755, nil)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}

	if fi.ModTime().Equal(fstime.Invented) {
		t.Errorf("an existing directory was given the invented time; it had %v", when)
	}
}

// With a clamp, these are stamped like everything else the build writes.
func TestAClampDecidesTheInventedTimeToo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	clamp := time.Unix(1600000000, 0)

	err := mkdirAllStamped(filepath.Join(root, "a", "b"), 0o755, &clamp)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(root, "a"))
	if err != nil {
		t.Fatal(err)
	}

	if !fi.ModTime().Equal(clamp) {
		t.Errorf("carried %v, want the clamp %v", fi.ModTime(), clamp)
	}
}
