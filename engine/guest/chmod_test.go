package guest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCopyChmodSetsTheModeOnWhatItCopied.
//
// `COPY --chmod=777 in/root .` gives the copied file that mode, whatever the
// source had. `tests/copy.earth+copy-chmod` copies one file four times
// asserting 644, 777, 600 and 666 in turn.
//
// The source's mode is what a copy carries by default, and that stays true: the
// flag replaces it rather than being folded into it, because the author wrote
// the number they want and not a modification of one they cannot see.
func TestCopyChmodSetsTheModeOnWhatItCopied(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	err := os.WriteFile(filepath.Join(root, "src"), []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = copyPath(root, filepath.Join(root, "src"), filepath.Join(root, "dst"),
		copyOpts{Chmod: "777"})
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(filepath.Join(root, "dst"))
	if err != nil {
		t.Fatal(err)
	}

	if got := fi.Mode().Perm(); got != 0o777 {
		t.Errorf("the copy has mode %04o, want 0777", got)
	}

	// A mode that is not a mode is refused, against the copy that asked for it
	// rather than as a number nobody can place.
	err = copyPath(root, filepath.Join(root, "src"), filepath.Join(root, "other"),
		copyOpts{Chmod: "nonsense"})
	if err == nil {
		t.Error("`--chmod=nonsense` was accepted, so the file has whatever mode" +
			" the source had and the build says it set one")
	}
}
