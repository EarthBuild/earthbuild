// Package sourceguard asks questions about a package's own source.
//
// **A guard that a thing is wired up, distinct from one that it works.** Two
// failures this repository has actually had look identical from a test suite:
// a helper written with the right behaviour and never called, and a helper
// called from a place a build never reaches. A behavioural test catches neither
// - it exercises the helper directly and passes.
//
// So these checks are paired: the behavioural test proves the thing works, and
// this proves somebody wired it up. Neither is worth much alone.
//
// In `internal` rather than copied per package because the copies diverge. The
// version this replaces says so itself: "three copies of one loop is where the
// fourth one silently starts skipping `_test.go` differently".
package sourceguard

import (
	"os"
	"path/filepath"
	"strings"
)

// NonTestFilesContaining counts occurrences of a needle in a directory's own
// non-test source, by file.
//
// Source-level and worth being plain about it: this proves a call exists, never
// that a build reaches it.
func NonTestFilesContaining(dir, needle string) (map[string]int, error) {
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
