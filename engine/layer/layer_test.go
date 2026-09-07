package layer_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/layer"
)

// write builds a tree and returns its root.
func write(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for p, content := range files {
		full := filepath.Join(root, p)
		err := os.MkdirAll(filepath.Dir(full), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(full, []byte(content), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Fix every mtime, directories included, so that a difference in the digest
	// is a difference in what was captured rather than in when the test ran.
	// Directories matter as much as files here: MkdirAll stamps them with the
	// wall clock, so a capture that includes directory mtimes - as this one does,
	// per green paper §3.3 - distinguishes two otherwise identical trees.
	stamp := time.Unix(1000000, 123456789)

	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		return os.Chtimes(p, stamp, stamp)
	})
	if err != nil {
		t.Fatal(err)
	}

	return root
}

func digest(t *testing.T, root string) string {
	t.Helper()

	id, _, err := layer.Digest(root)
	if err != nil {
		t.Fatal(err)
	}

	return id.String()
}

// The same tree captured twice is the same layer. Everything else here is a
// refinement of this.
func TestCaptureIsDeterministic(t *testing.T) {
	t.Parallel()

	files := map[string]string{"a": "1", "b/c": "2", "b/d": "3"}

	if x, y := digest(t, write(t, files)), digest(t, write(t, files)); x != y {
		t.Errorf("two captures of the same tree differ:\n%s\n%s", x, y)
	}
}

// Directory iteration order is a property of the filesystem, not of the layer.
// A digest that depended on it would make a cache hit depend on which machine
// wrote the tree.
func TestCaptureIsOrderIndependent(t *testing.T) {
	t.Parallel()

	a := write(t, map[string]string{"z": "1", "y": "2", "x": "3"})
	b := write(t, map[string]string{"x": "3", "y": "2", "z": "1"})

	if digest(t, a) != digest(t, b) {
		t.Error("capture depends on the order files were created")
	}
}

// I8, and the reason this engine exists in the form it does: cargo's incremental
// cache compares mtimes at nanosecond resolution, so a layer that records only
// whole seconds silently rebuilds the world.
func TestNanosecondMtimesAreCaptured(t *testing.T) {
	t.Parallel()

	base := time.Unix(1700000000, 0)

	roots := make([]string, 2)

	for i, ns := range []int{111111111, 111111222} {
		root := t.TempDir()

		p := filepath.Join(root, "f")
		err := os.WriteFile(p, []byte("same"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		stamp := base.Add(time.Duration(ns))
		err = os.Chtimes(p, stamp, stamp)
		if err != nil {
			t.Fatal(err)
		}

		// A filesystem that cannot store nanoseconds makes this unenforceable
		// (assumption A2), and the engine must say so rather than pass quietly.
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}

		if got := fi.ModTime().Nanosecond(); got != ns {
			t.Skipf("this filesystem stored %d ns, not %d: I8 is unenforceable here", got, ns)
		}

		roots[i] = root
	}

	if digest(t, roots[0]) == digest(t, roots[1]) {
		t.Error("two files differing only in mtime nanoseconds captured identically")
	}
}

// Content, mode and structure each change identity.
func TestCaptureDistinguishes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, root string)
	}{
		{"content", func(t *testing.T, root string) {
			t.Helper()

			err := os.WriteFile(filepath.Join(root, "a"), []byte("changed"), 0o600)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"mode", func(t *testing.T, root string) {
			t.Helper()

			// Distinct from the mode the fixture wrote, which is the whole
			// case: this asserts that *changing* a mode changes the layer's
			// identity, and it quietly stopped asserting anything when the
			// fixture and the chmod became the same 0o600.
			err := os.Chmod(filepath.Join(root, "a"), 0o400)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"a new path", func(t *testing.T, root string) {
			t.Helper()

			err := os.WriteFile(filepath.Join(root, "new"), []byte(""), 0o600)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"a removed path", func(t *testing.T, root string) {
			t.Helper()

			err := os.Remove(filepath.Join(root, "a"))
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"a renamed path", func(t *testing.T, root string) {
			t.Helper()

			err := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "renamed"))
			if err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := write(t, map[string]string{"a": "1", "b": "2"})
			before := digest(t, root)

			tc.mutate(t, root)

			if after := digest(t, root); after == before {
				t.Errorf("changing %s did not change the layer identity", tc.name)
			}
		})
	}
}

// A symlink is captured by its target, not by what the target contains -
// following it would make identity depend on something outside the layer.
func TestSymlinkTargetIsTheIdentity(t *testing.T) {
	t.Parallel()

	mk := func(target string) string {
		root := t.TempDir()
		err := os.Symlink(target, filepath.Join(root, "link"))
		if err != nil {
			t.Skip("symlinks unavailable here")
		}

		return root
	}

	if digest(t, mk("/a")) == digest(t, mk("/b")) {
		t.Error("symlinks to different targets captured identically")
	}
}

// Size is reported alongside the digest because the scheduler's cost model needs
// it, and a scheduler that estimates only time gets fleet placement wrong.
func TestSizeIsReported(t *testing.T) {
	t.Parallel()

	root := write(t, map[string]string{"a": "12345", "b/c": "678"})

	_, size, err := layer.Digest(root)
	if err != nil {
		t.Fatal(err)
	}

	if size < 8 {
		t.Errorf("size %d does not account for 8 bytes of content", size)
	}
}

// Two identical builds produce layers that differ, because creating a directory
// stamps it with the wall clock. That is faithful (green paper §3.3 records
// mtime per path) and it makes the *identity* run-dependent.
//
// Determinism screening (§6, experiment E14) compares two runs of the same step.
// Against the full identity it would flag every build that creates a directory -
// a screen with a 100% false positive rate, which is a screen nobody leaves on.
//
// So a capture carries two digests. ID is what is cached and restored, and keeps
// full fidelity. Content answers "did this step produce the same bytes?" and is
// what the screen compares.
func TestContentDigestIgnoresTimestamps(t *testing.T) {
	t.Parallel()

	mk := func() layer.Capture {
		root := t.TempDir()

		err := os.MkdirAll(filepath.Join(root, "d"), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(root, "d", "f"), []byte("x"), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		c, err := layer.Take(root)
		if err != nil {
			t.Fatal(err)
		}

		return c
	}

	a, b := mk(), mk()

	if a.ID == b.ID {
		t.Log("identities matched; this filesystem has coarse directory mtimes")
	}

	if a.Content != b.Content {
		t.Errorf("two identical trees have different content digests:\n%s\n%s", a.Content, b.Content)
	}
}

// The content digest must still distinguish content, or it screens nothing.
func TestContentDigestStillSeesChanges(t *testing.T) {
	t.Parallel()

	mk := func(body string) layer.Capture {
		root := t.TempDir()

		err := os.WriteFile(filepath.Join(root, "f"), []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		c, err := layer.Take(root)
		if err != nil {
			t.Fatal(err)
		}

		return c
	}

	if mk("one").Content == mk("two").Content {
		t.Error("different content produced the same content digest")
	}
}

// A path naming a single file must digest that file.
//
// The walk skips its own root, which is right for a layer - the root is the
// layer, not a member of it - and wrong when the root *is* a file. It produced a
// digest over zero entries, so every file in the world had the same identity.
// That reached the build as a COPY whose source could be edited freely without
// changing anything.
func TestSingleFileRootsAreDigested(t *testing.T) {
	t.Parallel()

	mk := func(body string) string {
		p := filepath.Join(t.TempDir(), "f")
		err := os.WriteFile(p, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		return p
	}

	a, _, err := layer.Digest(mk("one"))
	if err != nil {
		t.Fatal(err)
	}

	b, _, err := layer.Digest(mk("two"))
	if err != nil {
		t.Fatal(err)
	}

	if a == b {
		t.Error("two files with different contents digested identically")
	}

	var zero [32]byte
	if a == zero {
		t.Error("a single file digested to the zero value")
	}
}
