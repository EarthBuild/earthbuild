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

	err := os.WriteFile(filepath.Join(root, "layer"), make([]byte, 4096), 0o600)
	if err != nil {
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
		err := os.WriteFile(p, make([]byte, 4096), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	got, complete := Size(root)
	if !complete {
		t.Error("three small files did not finish within the budget")
	}

	// At least their contents, and usually more: a file costs whole blocks, and
	// what the disk gives back is what a collector is deciding about. See
	// occupies.
	if got < uint64(want) {
		t.Errorf("measured %d bytes, want at least %d", got, want)
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

func TestASizeIsANumberAndAUnit(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]uint64{
		"0":    0,
		"512":  512,
		"1k":   1 << 10,
		"512m": 512 << 20,
		"4g":   4 << 30,
		"4G":   4 << 30,
		"2t":   2 << 40,
		"100":  100,
	} {
		got, err := ParseSize(in)
		if err != nil {
			t.Errorf("ParseSize(%q): %v", in, err)

			continue
		}

		if got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}

	// A typo refused rather than ignored, which is this project's most recorded
	// failure: a mechanism that is off and one that found nothing look alike.
	for _, in := range []string{"", "4G8", "50%", "g", "-1", "4gb", "four"} {
		if got, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) = %d, want a refusal", in, got)
		}
	}
}

// A store of small files costs the disk far more than it holds, and a collector
// told the smaller number frees far less than it promised (E574).
func TestASizeIsWhatTheDiskGivesBack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	const files = 64

	for i := range files {
		p := filepath.Join(root, fmt.Sprintf("f%d", i))
		err := os.WriteFile(p, []byte("x"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	got := SizeAll(root)

	// Sixty-four one-byte files hold 64 bytes and occupy a block each.
	if got <= files {
		t.Errorf("%d one-byte files measured %d bytes, which is their contents"+
			" rather than what they cost the disk", files, got)
	}
}
