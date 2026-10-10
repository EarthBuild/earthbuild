package guest

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Clamping a tree reaches every entry, links and directories included.
//
// A step's output is the last thing in a build carrying wall-clock time, and it
// is what a reproducible build most wants pinned: the files it wrote are the
// work. A clamp that reached the regular files and left the directories - or
// followed a symlink and stamped its target twice - would produce a layer whose
// identity still moved, which is the whole of what the clamp is for (E549).
func TestClampingATreeReachesEveryEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.MkdirAll(filepath.Join(root, "opt", "thing"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(root, "opt", "thing", "a.txt"), []byte("hello"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Dangling on purpose: a clamp that follows links fails here rather than
	// quietly stamping the wrong entry, which is the failure that is hard to
	// see once it is in a digest.
	err = os.Symlink("/nowhere/at/all", filepath.Join(root, "opt", "link"))
	if err != nil {
		t.Fatal(err)
	}

	at := time.Unix(1_600_000_000, 0)

	err = clampTree(root, at)
	if err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{".", "opt", "opt/thing", "opt/thing/a.txt", "opt/link"} {
		fi, err := os.Lstat(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}

		if !fi.ModTime().Equal(at) {
			t.Errorf("%s carries %v and the clamp asked for %v",
				rel, fi.ModTime().UTC(), at.UTC())
		}
	}
}

// Clamping twice from different starting times gives the same tree.
//
// The property the clamp exists for, stated directly: two machines that ran the
// same work at different moments must end holding the same layer.
func TestClampingIsIndependentOfWhenItRan(t *testing.T) {
	t.Parallel()

	at := time.Unix(1_600_000_000, 0)

	times := func(root string) string {
		t.Helper()

		out := ""

		err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			rel, _ := filepath.Rel(root, p)
			out += rel + ":" + fi.ModTime().UTC().String() + "\n"

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		return out
	}

	build := func(when time.Time) string {
		t.Helper()

		root := t.TempDir()

		err := os.MkdirAll(filepath.Join(root, "d"), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(root, "d", "f"), []byte("x"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		// The moment the step ran, which is what differs between machines.
		for _, p := range []string{filepath.Join(root, "d", "f"), filepath.Join(root, "d"), root} {
			err = os.Chtimes(p, when, when)
			if err != nil {
				t.Fatal(err)
			}
		}

		err = clampTree(root, at)
		if err != nil {
			t.Fatal(err)
		}

		return times(root)
	}

	first := build(time.Unix(1_700_000_000, 0))
	second := build(time.Unix(1_800_000_000, 0))

	if first != second {
		t.Errorf("two clamped trees differ:\n%s\n%s", first, second)
	}
}
