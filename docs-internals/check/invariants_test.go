package check_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/docs-internals/check"
)

// §5 states the invariants and §5.1 says how each is enforced and tested.
//
// The two are maintained separately and cited from four documents and from the
// engine's comments, which is exactly the arrangement that drifts. The
// green-paper skill checks *cross-references* mechanically for that reason; the
// invariant table was not checked at all.
//
// It found the drift it was written for. I3 had **two rows**: one at its place
// in the order and one appended after I12, because somebody adding a second
// enforcement mechanism - "every field of ω reaches Κ₁, checked by reflection",
// the guard from E113 - appended a row rather than amending the one that was
// already there.
//
// Not a numbering collision: both rows are about key completeness, and both
// mechanisms are real. A table of one row per invariant with two rows for one
// invariant is a table whose shape says something untrue, and the shape is what
// a reader counts on when they scan for a number.
func TestTheInvariantTableHasOneRowPerInvariant(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join(docRoot, "green-paper.md"))
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range check.InvariantProblems(string(b)) {
		t.Error(p)
	}
}

// And the check itself is checked, by giving it text that is wrong.
//
// **A mutation is a different argument, not an edited file.** The previous
// version of this guard read the document and asserted, so mutation-checking it
// meant editing the document on disk and running a subprocess - and one such
// mutation replaced nothing, because `align-tables.py` had re-padded the row
// between writing the literal and running it. The test passed and the guard
// looked inert (E137).
//
// Nothing is copied here and nothing is restored, and "the mutation applied" is
// true by construction.
func TestTheInvariantCheckNoticesWhatItIsFor(t *testing.T) {
	t.Parallel()

	const good = "* **I1 (Purity).** …\n" +
		"* **I2 (Integrity).** …\n" +
		"| I1 | a | 1 | x |\n" +
		"| I2 | b | 2 | y |\n"

	if got := check.InvariantProblems(good); len(got) != 0 {
		t.Fatalf("a well-formed table was reported as wrong: %v", got)
	}

	for _, tc := range []struct {
		name, paper, want string
	}{{
		name:  "a duplicated row",
		paper: good + "| I2 | b again | 2 | y |\n",
		want:  "2 rows for I2",
	}, {
		name:  "a row for an invariant nobody states",
		paper: good + "| I9 | c | 3 | z |\n",
		want:  "row for I9",
	}, {
		name:  "a stated invariant with no row",
		paper: "* **I1 (Purity).** …\n* **I7 (Retry).** …\n| I1 | a | 1 | x |\n",
		want:  "states I7",
	}, {
		name:  "a paper that states nothing",
		paper: "| I1 | a | 1 | x |\n",
		want:  "states no invariants",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := check.InvariantProblems(tc.paper)
			if len(got) == 0 {
				t.Fatalf("no problem reported for %s", tc.name)
			}

			if !strings.Contains(strings.Join(got, "\n"), tc.want) {
				t.Errorf("the problem does not mention %q: %v", tc.want, got)
			}
		})
	}
}

// Every invariant the engine cites exists.
//
// The comments cite invariants constantly - `I1` for purity, `I3` for false
// hits, `I10` for honest refusal - and an invariant that was renumbered would
// leave every one of them pointing at a different rule while still reading
// plausibly. That is worse than a dangling section reference, which at least
// points at nothing.
func TestEveryInvariantTheCodeCitesExists(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join(docRoot, "green-paper.md"))
	if err != nil {
		t.Fatal(err)
	}

	stated := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\*\s+\*\*(I\d+)\s`).FindAllStringSubmatch(string(b), -1) {
		stated[m[1]] = true
	}

	// `I3` as a word, so `I3` in `API3` or a version number is not a citation.
	cite := regexp.MustCompile(`\bI(\d+)\b`)

	for _, path := range goSources(t, repoRoot) {
		src, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatal(err)
		}

		for _, line := range strings.Split(string(src), "\n") {
			if !strings.Contains(line, "//") {
				continue
			}

			for _, m := range cite.FindAllStringSubmatch(line, -1) {
				id := "I" + m[1]
				if !stated[id] {
					t.Errorf("%s cites %s, which §5 does not state:\n  %s",
						path, id, strings.TrimSpace(line))
				}
			}
		}
	}
}
