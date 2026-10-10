package main

import (
	"strings"
	"testing"
)

// A package that fails without a failing test is named.
//
// **The reporter knew and did not say.** It fails the run on any `fail` event,
// and `go test -json` emits one per failing *package* as well as per failing
// test, with `Test` empty. The duration table printed only events naming a test,
// so a build error, a panic or a timeout produced `test(s) failed` with not one
// `--- FAIL` anywhere in twenty thousand lines of log (E626).
func TestAPackageThatFailsWithoutATestIsNamed(t *testing.T) {
	t.Parallel()

	got := dedupe([]string{"example/broken", "example/broken", "example/other TestX"})

	if len(got) != 2 {
		t.Fatalf("dedupe kept %d of three entries, want 2: %v", len(got), got)
	}

	if got[0] != "example/broken" || got[1] != "example/other TestX" {
		t.Errorf("order or content changed: %v", got)
	}
}

// A failing test produces a package-level failure too, so the same package
// arrives repeatedly; the list has to stay readable.
func TestTheFailureListDoesNotRepeatItself(t *testing.T) {
	t.Parallel()

	in := []string{}
	for range 50 {
		in = append(in, "example/pkg")
	}

	if got := dedupe(in); len(got) != 1 {
		t.Errorf("fifty sightings of one package became %d lines", len(got))
	}
}

// And the empty case is empty rather than a line saying nothing.
func TestNothingFailedIsNothingPrinted(t *testing.T) {
	t.Parallel()

	if got := dedupe(nil); len(got) != 0 {
		t.Errorf("dedupe(nil) = %v, want no entries", got)
	}

	if strings.Join(dedupe([]string{}), "") != "" {
		t.Error("an empty list produced content")
	}
}
