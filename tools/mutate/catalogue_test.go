package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every anchor still matches exactly once.
//
// The sweep is slow - one `go test` per mutant - so it is not something a
// developer runs on every change, and a catalogue that had rotted would sit
// there reporting `ANCHOR` to nobody. This is the fast half: string matching,
// no compilation, part of the ordinary suite.
//
// Exactly once, not at least once. An anchor matching twice would mutate
// whichever came first, which is a sweep testing something other than what its
// entry says.
func TestEveryAnchorStillMatchesItsSource(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	for _, m := range Mutants {
		src, err := os.ReadFile(filepath.Join(root, m.File)) //nolint:gosec // from the catalogue
		if err != nil {
			t.Errorf("%s: %v\n  the file moved and the catalogue did not", m.Name, err)

			continue
		}

		if n := strings.Count(string(src), m.Anchor); n != 1 {
			t.Errorf("%s: the anchor matches %d times in %s, want 1"+
				"\n  %q"+
				"\n  the code moved; fix the entry rather than deleting it,"+
				" or the mechanism goes back to being unguarded",
				m.Name, n, m.File, m.Anchor)
		}
	}
}

// A mutant that does not change anything is not a mutant.
func TestNoMutantIsANoOp(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, m := range Mutants {
		if m.Anchor == m.Replacement {
			t.Errorf("%s: the replacement is the anchor", m.Name)
		}

		if m.Anchor == "" || m.Package == "" || m.Name == "" {
			t.Errorf("%v: an entry is missing a field", m)
		}

		if seen[m.Name] {
			t.Errorf("%s: two entries share a name, so a report cannot say"+
				" which survived", m.Name)
		}

		seen[m.Name] = true
	}
}

// repoRoot walks up to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		dir = filepath.Dir(dir)
	}

	t.Fatal("no go.mod above the working directory")

	return ""
}

// A sweep that is killed puts the file back.
//
// **Three times in one session** a mutation run outran its timeout and left a
// mutant applied: `waves.go` comparing without its transfer term, `delegating.go`
// pricing without a measurement. Each was caught by the next test run, and each
// could have been committed - the tool written to find defects introducing one.
//
// The comment on the restore said it happened "whatever happens, including a
// panic in this process". A `defer` does not run when the process is killed by a
// signal, which is exactly how a sweep ends when it is interrupted. *Failure
// class: a comment describing an intention* - in the tool that exists to catch
// that class (E348).
func TestASweepThatIsKilledPutsTheFileBack(t *testing.T) {
	t.Parallel()

	at := filepath.Join(t.TempDir(), "source.go")
	was := []byte("package p // the original\n")

	if err := os.WriteFile(at, was, 0o600); err != nil {
		t.Fatalf("%v", err)
	}

	// A mutant applied, as `run` applies one.
	holding(at, was)

	if err := os.WriteFile(at, []byte("package p // mutated\n"), 0o600); err != nil {
		t.Fatalf("%v", err)
	}

	// What a signal handler does.
	putBack()

	got, err := os.ReadFile(at)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if string(got) != string(was) {
		t.Errorf("after an interrupted sweep the file reads %q, want %q"+
			"\n  a stranded mutant is a defect the tool introduced (E348)",
			got, was)
	}

	// And nothing is put back twice: the ordinary path restores and clears, and
	// a signal arriving afterwards must not overwrite a file somebody has since
	// edited.
	if err = os.WriteFile(at, []byte("package p // edited since\n"), 0o600); err != nil {
		t.Fatalf("%v", err)
	}

	putBack()

	if got, _ = os.ReadFile(at); string(got) != "package p // edited since\n" {
		t.Errorf("a second restore overwrote a file nothing was holding: %q", got)
	}
}
