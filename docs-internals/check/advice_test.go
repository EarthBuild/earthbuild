package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No refusal tells an author to use a flag this CLI does not have.
//
// **The flag exists now** (E593), so this no longer refuses the phrase outright
// - it refuses it where the reader is already inside the native engine, which is
// advice that cannot apply where it is printed.
//
// Three messages said "build with `--engine=native`" - the macOS backend's
// refusal of `--isolate`, the plan-level refusal before a machine boots, and the
// buildkit engine's refusal of the flag. The native engine is reached by the
// `earth-native` binary; the flag is described in that binary's own doc comment
// as something that "will become `earthly --engine=native` once the flag is
// wired through", and it has not been.
//
// So all three sent an author to type something that prints a usage message
// (E403). It is E388's mirror: there, a flag existed and no document mentioned
// it; here, three documents mention a flag that does not exist.
//
// **Delete this test when the flag is wired.** It is a guard on a temporary
// state and says so, which is the difference between a scaffold and a lie.
func TestNoAdviceNamesAFlagThatDoesNotExist(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")

	var found []string

	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not this test's problem
		}

		if fi.IsDir() && (fi.Name() == skipGit || fi.Name() == skipModules || fi.Name() == skipTestdata) {
			return filepath.SkipDir
		}

		if fi.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}

		// This file names it in order to forbid it.
		if strings.HasSuffix(p, "advice_test.go") || strings.Contains(p, "earth-native") {
			return nil
		}

		// `p` came from walking this repository's own tree in a test.
		b, err := os.ReadFile(p) //nolint:gosec // our own source tree
		if err != nil {
			return nil //nolint:nilerr // ditto
		}

		// The flag exists now, so naming it is advice rather than a usage
		// message. What this still refuses is naming it from the *native
		// engine's own* packages, where the reader is already inside it.
		if strings.Contains(string(b), "--engine=native") && strings.Contains(p, "/engine/") {
			found = append(found, p)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(found) > 0 {
		t.Errorf("these name --engine=native from inside the native engine:\n  %s"+
			"\n  the native engine is what these packages already are, so telling"+
			" a reader to switch to it is advice that cannot apply where it is"+
			" printed", strings.Join(found, "\n  "))
	}
}
