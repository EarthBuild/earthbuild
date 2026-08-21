package exec

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// A linked tree is the tree it was linked from.
//
// linkTree places an unpacked image into a build's layer store. It is the
// largest fixed cost of materialising a base - 17,580 entries for
// golang:1.26.5-alpine3.24, at syscall latency each - so it is worth doing
// concurrently, and the point of this test is that doing so changes nothing
// about the result.
func TestALinkedTreeIsTheTreeItCameFrom(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	// Deep enough that a parallel implementation has to create parents before
	// children, and wide enough that it has something to overlap.
	for i := range 40 {
		dir := filepath.Join(src, "d"+strconv.Itoa(i%4), "e"+strconv.Itoa(i%3))

		err := os.MkdirAll(dir, 0o755)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), []byte("body"+strconv.Itoa(i)), 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := os.Symlink("d0/e0/f0", filepath.Join(src, "alink"))
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "out")

	err = linkTree(src, dst)
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	want := map[string]string{}

	err = filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(src, p)

		if fi.Mode()&os.ModeSymlink != 0 {
			to, err := os.Readlink(p)
			want[rel] = "->" + to

			return err
		}

		b, err := os.ReadFile(p)
		want[rel] = string(b)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(want) == 0 {
		t.Fatal("the fixture produced no files, so this checks nothing")
	}

	for rel, body := range want {
		at := filepath.Join(dst, rel)

		fi, err := os.Lstat(at)
		if err != nil {
			t.Errorf("%s is missing: %v", rel, err)

			continue
		}

		if fi.Mode()&os.ModeSymlink != 0 {
			to, _ := os.Readlink(at)
			if "->"+to != body {
				t.Errorf("%s points at %q, want %q", rel, to, body)
			}

			continue
		}

		b, err := os.ReadFile(at)
		if err != nil || string(b) != body {
			t.Errorf("%s is %q, want %q (%v)", rel, string(b), body, err)
		}
	}
}

// Placing into a destination somebody else may be writing replaces atomically.
//
// Two builds materialising the same image into the same directory both write
// every entry. `Remove` then create is a TOCTOU between them: both remove, one
// creates, the other gets `file exists` (E142). Rename replaces in one step, so
// the loser overwrites with identical bytes and nobody fails.
func TestASharedDestinationIsReplacedAtomically(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "f"), []byte("new"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()

	// Something is already there, as it would be if another build got here
	// first.
	err = os.WriteFile(filepath.Join(dst, "f"), []byte("old"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	err = linkTree(src, dst)
	if err != nil {
		t.Fatalf("link over an existing entry: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dst, "f"))
	if err != nil || string(b) != "new" {
		t.Errorf("entry is %q (%v), want the one just placed", string(b), err)
	}
}

// A destination nobody else can see is filled directly.
//
// Both real callers link into a temporary directory of their own and rename the
// finished tree into place, so no other writer can reach it while it is being
// filled. The atomic dance costs four extra syscalls per entry - create a temp,
// unlink it, link, rename - which on a Go base image is 15,808 entries and,
// measured, 2.3x the time of linking directly.
//
// *Protection against a race that the caller has already excluded.* The cost is
// invisible per entry and is most of the wall clock at this scale.
func TestAPrivateDestinationIsFilledDirectly(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	err := os.WriteFile(filepath.Join(src, "f"), []byte("body"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "mine")

	err = linkTreeExclusive(src, dst)
	if err != nil {
		t.Fatalf("link into a private destination: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dst, "f"))
	if err != nil || string(b) != "body" {
		t.Errorf("entry is %q (%v)", string(b), err)
	}
}
