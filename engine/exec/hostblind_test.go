package exec

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The host never asks the filesystem about a path under a handle's root.
//
// A materialised root is a path inside the *guest's* mount namespace. The host
// holds the string and cannot see what it names - `remoteHandle.Delta` says so
// in as many words - so a stat of it from here does not report what is there,
// it fails. Silently, and identically, whatever the build did.
//
// That is not hypothetical. `SAVE ARTIFACT --if-exists` decided absence with
// `os.Stat(filepath.Join(h.Root(), path))` on this side of the wire, so the
// stat failed for every path and the flag skipped every save it was ever
// applied to, including of files the build had just produced. It shipped that
// way because the only test of the flag used an absent path, where a correct
// skip and a broken one look the same.
//
// A syntactic guard suits it: the question has to travel to the guest, so a
// host-side `.Root()` next to an `os.` or `filepath.` call is the bug, and the
// engine currently contains none. If one is genuinely needed - a store the host
// really can read - do not add it here; give the handle a method that says so,
// because the point is that "can the host see this?" must be answered by a type
// rather than assumed by a caller.
func TestTheHostNeverStatsAPathInsideTheGuest(t *testing.T) {
	t.Parallel()

	// A root used as a path: named in the same breath as a filesystem call.
	root := regexp.MustCompile(`\.Root\(\)`)
	fsCall := regexp.MustCompile(`\b(os|filepath|ioutil)\.`)

	for _, pkg := range []string{".", "../cli"} {
		entries, err := os.ReadDir(pkg)
		if err != nil {
			t.Fatal(err)
		}

		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") ||
				strings.HasSuffix(name, "_test.go") {
				continue
			}

			b, err := os.ReadFile(filepath.Clean(filepath.Join(pkg, name)))
			if err != nil {
				t.Fatal(err)
			}

			for n, line := range strings.Split(string(b), "\n") {
				// This file names the pattern in order to look for it.
				if strings.Contains(line, "regexp.MustCompile") {
					continue
				}

				if root.MatchString(line) && fsCall.MatchString(line) {
					t.Errorf("%s/%s:%d asks the host's filesystem about a path"+
						" inside the guest, which cannot answer: %s",
						pkg, name, n+1, strings.TrimSpace(line))
				}
			}
		}
	}
}
