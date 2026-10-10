package bulk_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/bulk"
)

// A tree survives the round trip: contents, modes, symlinks and nesting.
//
// An export is whatever the author saved - `SAVE ARTIFACT /out` may be one file
// or a directory of them - so the carrier has to be a tree, and the thing that
// comes out has to be the thing that went in.
func TestATreeSurvivesTheRoundTrip(t *testing.T) {
	t.Parallel()

	from := t.TempDir()
	write(t, from, "top.txt", "one", 0o644)
	write(t, from, "sub/deep.txt", "two", 0o600)
	write(t, from, "sub/exe", "three", 0o755)

	if err := os.Symlink("top.txt", filepath.Join(from, "link")); err != nil {
		t.Fatal(err)
	}

	// **Named under the root's own base name**, whether the root is a file or a
	// directory. The receiver cannot tell the two apart from the stream, and
	// guessing is how `SAVE ARTIFACT /out` and `SAVE ARTIFACT /out.txt` come to
	// need different handling on the far side.
	base := filepath.Base(from)

	into := t.TempDir()
	roundTrip(t, from, into)

	for name, want := range map[string]string{
		base + "/top.txt": "one", base + "/sub/deep.txt": "two", base + "/sub/exe": "three",
	} {
		got, err := os.ReadFile(filepath.Join(into, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)

			continue
		}

		if string(got) != want {
			t.Errorf("%s is %q, wanted %q", name, got, want)
		}
	}

	target, err := os.Readlink(filepath.Join(into, base, "link"))
	if err != nil || target != "top.txt" {
		t.Errorf("the symlink came back as %q: %v", target, err)
	}

	fi, err := os.Lstat(filepath.Join(into, base, "sub/exe"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("the mode did not survive: %v %v", fi, err)
	}
}

// A single file is a tree of one, because that is what most exports are.
func TestASingleFileIsATree(t *testing.T) {
	t.Parallel()

	from := t.TempDir()
	write(t, from, "only.txt", "just this", 0o644)

	into := t.TempDir()
	roundTrip(t, filepath.Join(from, "only.txt"), into)

	got, err := os.ReadFile(filepath.Join(into, "only.txt"))
	if err != nil || string(got) != "just this" {
		t.Errorf("the file came back as %q: %v", got, err)
	}
}

// **The order is fixed**, so the same tree gives the same bytes: a directory
// listing has no order to promise and two machines will not agree on one.
func TestTheArchiveIsDeterministic(t *testing.T) {
	t.Parallel()

	from := t.TempDir()
	for _, n := range []string{"c", "a", "b"} {
		write(t, from, n, n, 0o644)
	}

	first, second := pack(t, from), pack(t, from)

	if !bytes.Equal(first, second) {
		t.Error("two packs of one tree differ")
	}
}

// An entry that would land outside the destination is refused. The archive is
// written inside the sandbox, so its names are the one thing here that the
// untrusted side chose.
func TestAnEntryThatEscapesIsRefused(t *testing.T) {
	t.Parallel()

	err := bulk.UnpackTree(strings.NewReader(escapingTar()), t.TempDir())
	if err == nil {
		t.Fatal("an entry was written outside the destination")
	}
}

func roundTrip(t *testing.T, from, into string) {
	t.Helper()

	var buf bytes.Buffer

	n, err := bulk.PackTree(from, &buf)
	if err != nil {
		t.Fatal(err)
	}

	if n != int64(buf.Len()) {
		t.Errorf("packed %d bytes and reported %d", buf.Len(), n)
	}

	if err := bulk.UnpackTree(&buf, into); err != nil {
		t.Fatal(err)
	}
}

func pack(t *testing.T, from string) []byte {
	t.Helper()

	var buf bytes.Buffer

	if _, err := bulk.PackTree(from, &buf); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func write(t *testing.T, root, name, body string, mode os.FileMode) {
	t.Helper()

	at := filepath.Join(root, name)

	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(at, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}
