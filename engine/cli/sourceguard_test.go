package cli

import (
	"os"
	"path/filepath"
	"strings"
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
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	found := map[string]int{}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}

		if n := strings.Count(string(b), needle); n > 0 {
			found[name] = n
		}
	}

	return found, nil
}

// writeFile writes a file and the directories above it.
func writeFile(path, body string) error {
	err := os.MkdirAll(filepath.Dir(path), 0o750)
	if err != nil {
		return err
	}

	return os.WriteFile(path, []byte(body), 0o600)
}
