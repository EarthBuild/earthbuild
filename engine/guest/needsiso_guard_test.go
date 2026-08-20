package guest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Nobody calls the isolation gate and throws the answer away.
//
// `NeedsIsolation` grew a `bool` return - true inside the namespace, false in
// the parent, which has already reported the child's outcome. **Go compiles a
// discarded return without a word**, so ten call sites went on running their
// bodies in the parent, unisolated, and the first one to actually touch a mount
// failed with `operation not permitted` about a machine that is fine.
//
// There is no `must_use` in Go and no vet check for this, so it is a source
// guard - and it is one of the few places a source guard is the *right* tool
// rather than a consolation, because the property is syntactic: the call is
// either the condition of an `if` or it is a bug.
//
// Worth noticing that this is the session's recurring failure class committed
// by the person removing it. Changing a function's contract and updating the
// call sites you can see is exactly "a rule applied at one of the two places it
// holds"; `guest.NeedsIsolation` and `NeedsIsolation` are the same function
// spelled two ways, and a regex that knew about one of them found ten of the
// sixteen.
func TestTheIsolationGateIsNeverIgnored(t *testing.T) {
	t.Parallel()

	bare := regexp.MustCompile(`(?m)^\s*(?:guest\.)?(?:N|n)eedsIsolation\(t\)\s*$`)

	root := ".."

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}

		b, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return err
		}

		// This file names the pattern in order to look for it.
		if strings.HasSuffix(path, "needsiso_guard_test.go") {
			return nil
		}

		if loc := bare.FindIndex(b); loc != nil {
			line := strings.Count(string(b[:loc[0]]), "\n") + 2

			t.Errorf("%s:%d calls the isolation gate and ignores its answer,"+
				"\n  so the body runs in the parent process, outside the namespace"+
				"\n  it asked for - write `if !NeedsIsolation(t) { return }`", path, line)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
