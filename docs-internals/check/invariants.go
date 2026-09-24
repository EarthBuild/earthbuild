// Package check holds the specification's mechanical checks.
//
// The checks are **functions over text**, not tests that read files. A test
// that opens a document and asserts can only be mutation-checked by editing the
// document on disk, running the test in a subprocess, and putting the document
// back - which is three ways to get a false pass, and one of them happened: a
// mutation whose literal no longer matched the re-padded file replaced nothing,
// the test passed, and the guard looked inert (E137).
//
// Given a function, a mutation is a different argument. Nothing is copied,
// nothing is restored, and "the mutation applied" is true by construction
// because the input was constructed.
package check

import (
	"fmt"
	"regexp"
	"sort"
)

var (
	statedRe = regexp.MustCompile(`(?m)^\*\s+\*\*(I\d+)\s`)
	rowRe    = regexp.MustCompile(`(?m)^\|\s*(I\d+)\s*\|`)

	// A section number may carry a letter suffix (`3.3a`) or a hyphenated one
	// (`2a-bis`). Stopping at the letter is one of the two mistakes these
	// checks exist to stop repeating (E128).
	headingRe = regexp.MustCompile(`(?m)^#+\s+([0-9A-E][0-9.]*[a-z]*(?:-[a-z]+)?)\.?\s`)
	// A *definition* is `(n.m)` followed by whitespace and the equation. A
	// citation at the start of a line - `(4.2), so a scheduler may choose` - is
	// not one, and without the trailing `\s` the extractor reported the
	// specification as defining `(4.2)` twice (E144).
	equationRe = regexp.MustCompile(`(?m)^\(([0-9A-E]\.[0-9]+[a-z]?)\)\s`)
	sigilRe    = regexp.MustCompile(`§([0-9A-E][0-9.]*[0-9a-z]*(?:-[a-z]+)?)`)
	// `[1-9]` after the dot, not `[0-9]`: equations are numbered from one, and
	// the loose form read `--- FAIL: TestX (0.00s)` - what `go test` prints, and
	// ordinary content for a fixture - as a reference to equation 0.00s. The
	// `[a-z]?` is there for `(3.4a)` and is what let the trailing `s` through.
	//
	// Narrower on purpose. Every earlier correction in this package widened a
	// pattern that missed something; this is the other failure, and it is the
	// worse one for a guard: a check that misses goes unnoticed, a check that
	// accuses gets deleted (E158d).
	parenRe = regexp.MustCompile(`\((?:green paper )?([0-9A-E]\.[1-9][0-9]*[a-z]?)\)`)
	// Matched against the text *before* a citation, so `plan-native-engine.md
	// §2a-bis` is a plan reference and a `§3.3` earlier on the same line is not.
	plannedRe = regexp.MustCompile(`plan[^ ]*\s+$`)

	// Test-plan items are lettered strands and numbers: `### a16. Capability
	// gate test`. Cited from the specification as `test-plan a16`.
	planItemRe = regexp.MustCompile(`(?m)^#+\s+([a-z]\d+)\.\s`)
	planCiteRe = regexp.MustCompile(`test-plan ([a-z]\d+)`)

	// An experiment is `## E76 - …` or `### E5b - …`. **Any heading level**: E5b
	// and E5c are sub-sections of E5, and a pattern anchored to `##` reported
	// the invariant table as citing two experiments that do not exist -
	// including the one testing I3, which is the invariant the whole design
	// rests on. The fourth pattern in this package to have been too narrow
	// (E128, E144, E149).
	// A test cited in prose, and a test declared in Go. The citation pattern
	// deliberately requires a capital after `Test`, so that the word "Testing"
	// or a sentence beginning "Test the" is not read as a name.
	testCiteRe = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9]*`)
	testDeclRe = regexp.MustCompile(`(?m)^func (Test[A-Z][A-Za-z0-9]*)\(`)

	expDefRe  = regexp.MustCompile(`(?m)^#+\s+(E\d+[a-z]?)\s`)
	expCiteRe = regexp.MustCompile(`\bE(\d+[a-z]?)\b`)
)

// InvariantProblems reports what is wrong between §5's statements and §5.1's
// table, or nothing.
//
// §5 states the invariants and §5.1 says how each is enforced and tested. They
// are maintained separately and cited from four documents and from the engine's
// comments, which is the arrangement that drifts.
func InvariantProblems(paper string) []string {
	stated := map[string]bool{}
	for _, m := range statedRe.FindAllStringSubmatch(paper, -1) {
		stated[m[1]] = true
	}

	rows := map[string]int{}
	for _, m := range rowRe.FindAllStringSubmatch(paper, -1) {
		rows[m[1]]++
	}

	var out []string

	if len(stated) == 0 {
		return []string{"§5 states no invariants, so nothing here is checking anything"}
	}

	for id, n := range rows {
		if n > 1 {
			out = append(out, fmt.Sprintf(
				"§5.1 has %d rows for %s: one row per invariant is what a reader scanning"+
					" for a number counts on, and a second mechanism belongs in the existing"+
					" row rather than after it", n, id))
		}

		if !stated[id] {
			out = append(out, fmt.Sprintf("§5.1 has a row for %s and §5 does not state it", id))
		}
	}

	for id := range stated {
		if rows[id] == 0 {
			out = append(out, fmt.Sprintf(
				"§5 states %s and §5.1 does not say how it is enforced or tested;"+
					" a mark of **[GAP]** in the row is the way to say it is not yet", id))
		}
	}

	return out
}

// DuplicateEquations reports numbers the specification defines more than once.
//
// An equation number is a name, cited from prose, from three other documents
// and from the engine's comments. Two definitions under one number means every
// citation of it names two different things and reads plausibly as either -
// which is worse than a dangling reference, because a dangling one points at
// nothing and this points at the wrong thing half the time.
//
// Nothing checked it. The invariant table is checked for exactly this (E137)
// and equations are the specification's other numbered namespace.
func DuplicateEquations(paper string) []string {
	seen := map[string]int{}
	for _, m := range equationRe.FindAllStringSubmatch(paper, -1) {
		seen[m[1]]++
	}

	var out []string

	for id, n := range seen {
		if n > 1 {
			out = append(out, fmt.Sprintf(
				"(%s) is defined %d times: a number is a name, and two definitions"+
					" under one name means every citation of it reads plausibly as either",
				id, n))
		}
	}

	sort.Strings(out)

	return out
}

// Experiments is every experiment the log defines, at any heading level.
func Experiments(doc string) map[string]bool {
	out := map[string]bool{}

	for _, m := range expDefRe.FindAllStringSubmatch(doc, -1) {
		out[m[1]] = true
	}

	return out
}

// ExperimentCitations reports references to experiments that were never run.
//
// The documents cite them constantly - an invariant's row in §5.1 says which
// experiment tests it, the plan says which one a milestone waits on - and an
// experiment is the only evidence a claim in those tables has. **A citation of
// an experiment nobody ran is a claim with no evidence that reads exactly like
// one with evidence**.
func ExperimentCitations(where, doc string, defined map[string]bool) []string {
	var out []string

	seen := map[string]bool{}

	for _, m := range expCiteRe.FindAllStringSubmatch(doc, -1) {
		id := "E" + m[1]
		if defined[id] || seen[id] {
			continue
		}

		seen[id] = true

		out = append(out, fmt.Sprintf(
			"%s cites %s, which the experiment log does not define", where, id))
	}

	sort.Strings(out)

	return out
}
