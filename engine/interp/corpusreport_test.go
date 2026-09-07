package interp_test

import (
	"strings"
	"testing"
)

// Every cause in the corpus report names a place.
//
// The report has two lists. The first, "unimplemented: this is the work",
// prints one example site per cause, and the code says why: *"a construct name
// is not a place to go and look. `FROM, 5 causes, 376 targets` was read three
// times as propagation from something else without anyone checking."*
//
// The second is headed **"refused as invalid input: verify these are right"**
// and printed no site at all. So the list that asks to be verified was the one
// that could not be, and it is the list where being wrong is expensive: a
// refusal that says the Earthfile is wrong is only worth discounting if it *is*
// right, and this branch has already found one that was not - a quoted
// `--load` reference reported as an undeclared import alias, sitting in that
// list under the word `cycle`'s neighbours for however long.
//
// Twelve targets are refused for a `cycle`, which is a great many cycles for a
// corpus of real Earthfiles, and there was no way to look at one.
func TestEveryCorpusCauseNamesAPlace(t *testing.T) {
	t.Parallel()

	got := causeReport("cycle", 12, 12, map[string]bool{
		"tests/a/Earthfile:4": true,
		"tests/b/Earthfile:9": true,
	})

	if len(got) < 2 {
		t.Fatalf("a cause with sites rendered no example:\n%s", strings.Join(got, "\n"))
	}

	if !strings.Contains(got[0], "cycle") || !strings.Contains(got[0], "12") {
		t.Errorf("the first line is not the cause: %q", got[0])
	}

	// The lowest-sorting one, so two runs over one corpus report the same site
	// (I12). Picking whichever the map yielded first would make the report vary
	// between runs of an unchanged tree, which this branch has now hit three
	// times.
	if !strings.Contains(got[1], "tests/a/Earthfile:4") {
		t.Errorf("the example is not the first site in order: %q", got[1])
	}
}

// A cause with no recorded site renders one line, not a blank one.
//
// Some refusals are raised before anything has a location - a whole file that
// will not parse. A dangling indented line under those reads as a site that
// went missing rather than one that never existed.
func TestACauseWithNoSiteRendersOneLine(t *testing.T) {
	t.Parallel()

	got := causeReport("parse error", 2, 4, nil)

	if len(got) != 1 {
		t.Errorf("a cause with no site rendered %d lines:\n%s", len(got), strings.Join(got, "\n"))
	}
}
