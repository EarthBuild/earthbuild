package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestAFullDiskSaysWhoFilledIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "layer"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}

	hint := FullHint(fmt.Errorf("write a layer: %w", syscall.ENOSPC), root)
	if hint == "" {
		t.Fatal("an out-of-space error got no explanation")
	}

	// Each of these is a question somebody asks at three in the morning, and a
	// hint that answers two of them sends them looking for the third.
	for _, want := range []string{
		root,                 // which store
		"KiB",                // how big it is
		"nothing collects",   // why it got that way
		"deleting the store", // what to do
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("the hint never mentions %q:\n%s", want, hint)
		}
	}
}

// The rule the other hints follow: say nothing about errors you do not
// understand, or every unrelated failure grows a paragraph about disk space.
func TestOnlyAFullDiskIsExplained(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, err := range []error{
		syscall.EACCES,
		syscall.ENOENT,
		errors.New("something else entirely"),
	} {
		if hint := FullHint(err, root); hint != "" {
			t.Errorf("%v was explained as a full disk:\n%s", err, hint)
		}
	}

	if hint := FullHint(syscall.ENOSPC, ""); hint != "" {
		t.Errorf("a store with no root was described:\n%s", hint)
	}
}

func TestSizeSaysWhenItIsAFloor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	want := 3 * 4096
	for i := range 3 {
		p := filepath.Join(root, fmt.Sprintf("f%d", i))
		if err := os.WriteFile(p, make([]byte, 4096), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, complete := Size(root)
	if !complete {
		t.Error("three small files did not finish within the budget")
	}

	if got != uint64(want) {
		t.Errorf("measured %d bytes, want %d", got, want)
	}

	if _, complete := Size(""); complete {
		t.Error("a store with no root reported a complete measurement")
	}
}

func TestFreeIsWhatThisUserMayHave(t *testing.T) {
	t.Parallel()

	got, err := Free(t.TempDir())
	if err != nil {
		t.Fatalf("could not ask the filesystem: %v", err)
	}

	if got == 0 {
		t.Skip("this filesystem has nothing left, which is its own problem")
	}

	if _, err := Free(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a path that is not there reported free space")
	}
}

func TestHumanReadsLikeADiskLabel(t *testing.T) {
	t.Parallel()

	for n, want := range map[uint64]string{
		0:             "0 B",
		512:           "512 B",
		1024:          "1.0 KiB",
		1536:          "1.5 KiB",
		1024 * 1024:   "1.0 MiB",
		13 * 1 << 30:  "13.0 GiB",
		3<<40 + 1<<39: "3.5 TiB",
	} {
		if got := human(n); got != want {
			t.Errorf("human(%d) = %q, want %q", n, got, want)
		}
	}
}
