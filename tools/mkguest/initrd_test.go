package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The archive is the same bytes for the same tree, however many times it is
// built and wherever the tree happens to sit.
//
// **Which `cpio` is not.** BSD cpio writes each file's real inode number into
// the newc header, so packing the same tree from two temporary directories
// gives two different archives - the first thing tried here, and it differed at
// byte 13. GNU cpio has `--reproducible` and macOS does not have GNU cpio, so
// the archive is written here instead: one fewer tool, and reproducible by
// construction rather than by flag.
func TestTheArchiveIsTheSameBytesEveryTime(t *testing.T) {
	t.Parallel()

	first := writeInto(t, tree(t))
	second := writeInto(t, tree(t))

	if !bytes.Equal(first, second) {
		t.Errorf("two builds of one tree differ: %d bytes against %d",
			len(first), len(second))
	}
}

// Entries come out sorted, because directory order is allocation order and so
// is neither stable nor meaningful.
func TestEntriesAreSorted(t *testing.T) {
	t.Parallel()

	got := string(writeInto(t, tree(t)))

	at := func(name string) int { return strings.Index(got, name) }

	if at("earth-guestd") > at("init") {
		t.Error("entries are not in sorted order")
	}
}

// A newc archive ends with the trailer, and a kernel that does not find one
// treats the whole initramfs as truncated.
func TestTheArchiveIsTerminated(t *testing.T) {
	t.Parallel()

	if !strings.Contains(string(writeInto(t, tree(t))), "TRAILER!!!") {
		t.Error("no trailer, so the kernel reads the archive as truncated")
	}
}

// tree is a guest root with the shape a real one has: two executables and the
// mount points PID 1 needs.
func tree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	for _, d := range []string{"proc", "sys", "dev"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, f := range []string{"init", "earth-guestd"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("#!/x\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func writeInto(t *testing.T, root string) []byte {
	t.Helper()

	var buf bytes.Buffer

	if err := writeCPIO(&buf, root); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}
