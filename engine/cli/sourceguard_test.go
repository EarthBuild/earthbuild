package cli

import (
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/internal/sourceguard"
)

// nonTestFilesContaining counts occurrences of a needle in the package's own
// non-test source, by file.
//
// Three tests in this package ask a question about the *code* rather than about
// its behaviour - does anything call this, is this constructed twice - and each
// had walked the directory itself. Three copies of one loop is where the fourth
// one silently starts skipping `_test.go` differently.
//
// Source-level checks and what they are worth: they prove a call exists, never
// that a build reaches it. Every one of them is paired with a behavioural test
// elsewhere, and the pairing is the point - the behavioural test proves the
// thing works, this proves somebody wired it up.
func nonTestFilesContaining(dir, needle string) (map[string]int, error) {
	return sourceguard.NonTestFilesContaining(dir, needle)
}

// writeFile writes a file and the directories above it.
func writeFile(path, body string) error {
	err := os.MkdirAll(filepath.Dir(path), 0o750)
	if err != nil {
		return err
	}

	return os.WriteFile(path, []byte(body), 0o600)
}
