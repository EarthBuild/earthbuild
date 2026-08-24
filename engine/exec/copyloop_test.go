package exec

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink to an ancestor does not send the export round for ever.
//
// `copyDir` walks with `filepath.Walk`, which lstats: a symlink is not a
// directory to it, so the entry goes to `copyOut` - which *stats*, sees a
// directory through the link, and calls `copyDir` on it. That walk finds the
// same link again one level down, and the engine dies with
//
//	fatal error: stack overflow
//
// found on the eightieth corpus file, `git-clone.earth`, whose checkout has one
// (E452).
//
// **Two functions disagreeing about what a symlink is.** Neither is wrong on its
// own; the pair is a loop, and a directory that contains a link to its own
// parent is an ordinary thing for a checkout to hold.
func TestASymlinkToAnAncestorDoesNotLoop(t *testing.T) {
	t.Parallel()

	src := t.TempDir()

	inner := filepath.Join(src, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}

	err := os.WriteFile(filepath.Join(inner, "f"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// The link that closes the circle: inner/up -> src.
	err = os.Symlink(src, filepath.Join(inner, "up"))
	if err != nil {
		t.Skipf("this filesystem will not make a symlink: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "out")

	// The link is copied, not followed and not refused.
	//
	// The first version of this accepted a refusal as "an answer" too, on the
	// grounds that not looping was the point - and the mutation sweep walked
	// straight through the hole: deleting the branch that copies the link leaves
	// `os.ReadFile` following it, failing on a directory, and returning an error
	// the test called success. **A test with two acceptable outcomes tests
	// neither of them.**
	err = copyOut(src, dst)
	if err != nil {
		t.Fatalf("exporting a tree containing a link to its own parent: %v", err)
	}

	// The link arrived as a link rather than as a second copy of everything
	// under it.
	fi, err := os.Lstat(filepath.Join(dst, "inner", "up"))
	if err != nil {
		t.Fatalf("the link is not in the output: %v", err)
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("inner/up arrived as %s, and it is a symlink", fi.Mode())
	}
}
