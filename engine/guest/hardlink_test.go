package guest

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func linkCount(t *testing.T, path string) uint64 {
	t.Helper()

	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}

	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("this platform does not report link counts")
	}

	return uint64(st.Nlink)
}

// Two paths that share an inode still share one after a copy.
//
// The sibling of E88's whiteout, found by the same question: what else does
// `copyTree` quietly drop? It copies every regular file by opening it and
// writing the bytes somewhere else, so two names for one inode become two
// independent files.
//
// `layer.Take` records the link, and says why in as many words: *"two paths
// sharing an inode are not two independent copies, and a layer that recorded
// them as such would lose the link on restore."* It records it and the copy
// loses it, which is the same shape as `copyTree` documenting the mtime
// invariant it broke for directories (E87) - **the comment describing the
// property and the code beneath it are maintained by different people at
// different times, and one of them is a person who is not reading the other**.
//
// The cost is not subtle. `busybox` is one binary with four hundred names
// hardlinked to it, and `alpine`'s `/bin` is exactly that: a delta carrying it
// becomes four hundred copies of the same executable. It is also a second
// reason a stored layer does not re-digest to its own name, since the digest
// records what the copy did not reproduce.
func TestAHardLinkSurvivesACopy(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("body\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Link(filepath.Join(src, "a.txt"), filepath.Join(src, "b.txt"))
	if err != nil {
		t.Skipf("hard links are not available here: %v", err)
	}

	err = copyTree(src, dst, copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if got := linkCount(t, filepath.Join(dst, "a.txt")); got != 2 {
		t.Errorf("a.txt has %d links at the destination, not 2 - the copy made two files", got)
	}

	// And they are the *same* inode, not merely two files each with a link
	// count of two, which is what a naive fix produces.
	a, err := os.Lstat(filepath.Join(dst, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.Lstat(filepath.Join(dst, "b.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if !os.SameFile(a, b) {
		t.Error("the two paths at the destination are different files")
	}
}

// A copy does not link files that were never linked.
//
// The arm that stops the fix being "link anything with equal contents", which
// would be a deduplicating copy rather than a faithful one - and would make two
// files that a later step writes to independently start sharing.
func TestIdenticalFilesAreNotLinkedTogether(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	for _, name := range []string{"a.txt", "b.txt"} {
		err := os.WriteFile(filepath.Join(src, name), []byte("identical\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := copyTree(src, dst, copyOpts{})
	if err != nil {
		t.Fatal(err)
	}

	a, err := os.Lstat(filepath.Join(dst, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.Lstat(filepath.Join(dst, "b.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if os.SameFile(a, b) {
		t.Error("two files with the same contents were linked into one")
	}
}
