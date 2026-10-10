package layer_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/engine/layer"
)

func writeAt(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(p), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(p, []byte(body), mode)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Chmod(p, mode)
	if err != nil {
		t.Fatal(err)
	}
}

// PathDigest is what an observation records about one path.
//
// 𝑅 maps a path to "the content digest of each" (green paper §3.4), and
// `Consistent` compares that against `BaseView.Digest`. Both sides must be one
// function or the comparison is between two different questions - which is E113
// again, and this one has not been written twice yet.
//
// **Times are excluded.** A layer's identity comes in two flavours already:
// `Capture.ID` includes mtimes and `Capture.Content` does not. A view is built
// by materialising a stack, and two materialisations of one layer set the same
// bytes at different moments - so a digest carrying mtime would make every
// prediction inconsistent with every base, and L2 would never hit while
// appearing to work.
//
// Everything else in §3.3 is in. A `chmod` on a file a step read changes what
// the step would do with it; a view that ignored mode would serve a cached
// result computed when the file was not executable.
func TestPathDigestRecordsWhatWasReadAndNotWhenItWasWritten(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")

	writeAt(t, p, "body\n", 0o644)

	first, err := layer.PathDigest(p)
	if err != nil {
		t.Fatal(err)
	}

	if first == (ir.NodeID{}) {
		t.Fatal("the digest is the zero value, which is what an absent path returns")
	}

	t.Run("the same bytes written later digest the same", func(t *testing.T) {
		t.Parallel()

		later := filepath.Join(t.TempDir(), "a.txt")
		writeAt(t, later, "body\n", 0o644)

		at := time.Unix(1_600_000_000, 0)

		err := os.Chtimes(later, at, at)
		if err != nil {
			t.Fatal(err)
		}

		got, err := layer.PathDigest(later)
		if err != nil {
			t.Fatal(err)
		}

		if got != first {
			t.Error("two copies of one file digest differently, so no prediction" +
				" made against one base could ever verify against another")
		}
	})

	t.Run("different bytes digest differently", func(t *testing.T) {
		t.Parallel()

		other := filepath.Join(t.TempDir(), "a.txt")
		writeAt(t, other, "edited\n", 0o644)

		got, err := layer.PathDigest(other)
		if err != nil {
			t.Fatal(err)
		}

		if got == first {
			t.Error("an edited file digests the same as the original")
		}
	})

	t.Run("a mode change digests differently", func(t *testing.T) {
		t.Parallel()

		exe := filepath.Join(t.TempDir(), "a.txt")
		writeAt(t, exe, "body\n", 0o755)

		got, err := layer.PathDigest(exe)
		if err != nil {
			t.Fatal(err)
		}

		if got == first {
			t.Error("the same bytes made executable digest the same, so a step" +
				" that ran the file would hit against a base where it cannot")
		}
	})

	t.Run("a symlink is not its target", func(t *testing.T) {
		t.Parallel()

		d := t.TempDir()
		writeAt(t, filepath.Join(d, "a.txt"), "body\n", 0o644)

		link := filepath.Join(d, "link")

		err := os.Symlink("a.txt", link)
		if err != nil {
			t.Skipf("symlinks are not available here: %v", err)
		}

		got, err := layer.PathDigest(link)
		if err != nil {
			t.Fatal(err)
		}

		if got == first {
			t.Error("a symlink digests as the file it points at, so the view" +
				" cannot tell a link from what it names")
		}
	})

	t.Run("an absent path is an error, not a zero digest", func(t *testing.T) {
		t.Parallel()

		_, err := layer.PathDigest(filepath.Join(dir, "nothing-here"))
		if err == nil {
			t.Error("a missing path returned a digest, and the zero value is a" +
				" digest a caller could compare against")
		}
	})
}
