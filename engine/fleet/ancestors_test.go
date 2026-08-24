package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

// A lazily placed entry brings its ancestors' modes with it.
//
// **`MkdirAll(dir, 0o755)` invented them.** Only a directory that was itself
// faulted in ever received its real mode, so a lazy base differed from an eager
// one by the modes of the directories nobody asked for - and §3.3 counts a mode
// among what a layer records, so the two produced different layers (E631).
//
// It stayed hidden because 0o755 is what a fixture usually writes. Tightening
// some test trees to 0o750 made lazy and eager disagree, which is exactly what
// TestARealStepOnALazyBaseProducesTheRightLayer exists to notice.
func TestAPlacedEntryBringsItsAncestorsModes(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "base")

	// A source tree whose directories are deliberately not the default.
	deep := filepath.Join(src, "usr", "lib", "x86_64")

	err := os.MkdirAll(deep, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	for dir, mode := range map[string]os.FileMode{
		filepath.Join(src, "usr"):                  0o701,
		filepath.Join(src, "usr", "lib"):           0o750,
		filepath.Join(src, "usr", "lib", "x86_64"): 0o755,
	} {
		err := os.Chmod(dir, mode)
		if err != nil {
			t.Fatal(err)
		}
	}

	from := filepath.Join(deep, "libc.so")
	to := filepath.Join(dst, "usr", "lib", "x86_64", "libc.so")

	err = os.WriteFile(from, []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = makeAncestors(from, to)
	if err != nil {
		t.Fatalf("making room failed: %v", err)
	}

	for rel, want := range map[string]os.FileMode{
		"usr":            0o701,
		"usr/lib":        0o750,
		"usr/lib/x86_64": 0o755,
	} {
		fi, err := os.Lstat(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("%s was not created: %v", rel, err)

			continue
		}

		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s has mode %o, want %o"+
				"\n  an ancestor invented with a fixed mode makes a lazy base"+
				" differ from an eager one, and a mode is part of the layer",
				rel, got, want)
		}
	}
}

// A directory already in place keeps the mode it was given, rather than being
// rewritten by a guess about its source.
func TestAnExistingAncestorIsNotRewritten(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	dst := t.TempDir()

	err := os.MkdirAll(filepath.Join(src, "a"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(filepath.Join(dst, "a"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = makeAncestors(filepath.Join(src, "a", "f"), filepath.Join(dst, "a", "f"))
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(filepath.Join(dst, "a"))
	if err != nil {
		t.Fatal(err)
	}

	if got := fi.Mode().Perm(); got != 0o755 {
		t.Errorf("an ancestor already placed was rewritten to %o", got)
	}
}
