package check

import (
	"fmt"
	"sort"
	"strings"
)

// Sections is every section number a document defines.
//
// Extracted with a pattern that has been wrong twice, which is why it is a
// function over text rather than a step inside a test: `## 2. State` defines
// section 2, so the trailing dot has to go, and `2a-bis` is a section whose
// name does not stop at the first letter. Both mistakes made a *resolving*
// citation look broken, and either could as easily have made a broken one look
// fine (E128).
func Sections(doc string) map[string]bool {
	out := map[string]bool{}

	for _, m := range headingRe.FindAllStringSubmatch(doc, -1) {
		out[strings.TrimSuffix(m[1], ".")] = true
	}

	return out
}

// Equations is every numbered equation a document defines.
//
// The specification numbers them `(n.m)` at the start of a line inside a `text`
// fence, which is the form the green-paper skill fixes and the form the code
// cites back.
func Equations(doc string) map[string]bool {
	out := map[string]bool{}

	for _, m := range equationRe.FindAllStringSubmatch(doc, -1) {
		out[m[1]] = true
	}

	return out
}

// CitationProblems reports every reference in one source file that names
// nothing.
//
// The engine's comments cite the specification constantly - `§3.3` for what a
// layer records, `(C.3)` for the wire vocabulary - and those citations are why
// the comments are worth reading: they say where a rule comes from rather than
// restating it. They rot silently.
//
// Two forms, because the engine uses two. `§n.m` names a section; `(n.m)` names
// an equation, and the same parenthesised form names *sections* when the
// sentence already says "green paper", so it resolves against either.
func CitationProblems(path, source string, paper, plan, equations map[string]bool) []string {
	var out []string

	for _, line := range strings.Split(source, "\n") {
		for _, m := range parenRe.FindAllStringSubmatch(line, -1) {
			if !paper[m[1]] && !equations[m[1]] {
				out = append(out, fmt.Sprintf(
					"%s cites (%s), which the green paper has as neither a section nor"+
						" an equation:\n  %s", path, m[1], strings.TrimSpace(line)))
			}
		}

		for _, loc := range sigilRe.FindAllStringSubmatchIndex(line, -1) {
			ref := strings.TrimSuffix(line[loc[2]:loc[3]], ".")

			// Per citation, not per line. Deciding by the whole line meant a
			// comment naming a section of each - `§3.3 … plan-native-engine.md
			// §2a-bis` - checked *both* against the plan and reported the
			// paper's section as missing.
			//
			// Found the moment this became a function taking text: the line was
			// written as an example of citations that resolve, and it did not.
			// A check that reads files could only have been given that input by
			// somebody writing it into a file first (E138).
			where, name := paper, "the green paper"
			if plannedRe.MatchString(line[:loc[0]]) {
				where, name = plan, "the plan"
			}

			if !where[ref] {
				out = append(out, fmt.Sprintf("%s cites §%s, which %s does not have:\n  %s",
					path, ref, name, strings.TrimSpace(line)))
			}
		}
	}

	return out
}

// TestPlanItems is every item the test plan defines: `### a16. Capability gate`.
func TestPlanItems(doc string) map[string]bool {
	out := map[string]bool{}

	for _, m := range planItemRe.FindAllStringSubmatch(doc, -1) {
		out[m[1]] = true
	}

	return out
}

// TestPlanCitations reports references to test-plan items that do not exist.
//
// §5.1 names, for each invariant, what tests it - and for two of them the answer
// is a test-plan item rather than a suite in the tree. Those are the invariants
// whose enforcement is *planned*, so the citation is the only thing connecting
// the promise to the work, and a citation nobody checks is a promise nobody
// checks.
//
// The third document to be brought into this: the specification's internal
// cross-references were checked by hand, the engine's citations into it by
// E128, and its citations *out* by nothing.
func TestPlanCitations(paper string, items map[string]bool) []string {
	var out []string

	for _, m := range planCiteRe.FindAllStringSubmatch(paper, -1) {
		if !items[m[1]] {
			out = append(out, fmt.Sprintf(
				"the specification cites test-plan %s, which the test plan does not define",
				m[1]))
		}
	}

	return out
}

// TestNames finds the Go tests a document cites as evidence.
//
// The documents name tests constantly - a stage's state, an invariant's row, a
// milestone's proof - and a test is often the *only* evidence for the sentence
// around it. A name that no longer exists is a claim with nothing behind it
// that reads exactly like a claim with something behind it, which is the
// argument E149 made about experiments.
//
// Tests get renamed. This one was written the same hour two were cited in the
// plan, and the citations were correct; the point is that nothing would have
// said so a month later.
func TestNames(doc string) []string {
	seen := map[string]bool{}

	var out []string

	// Outside fenced blocks. A `TestX` in an example is illustration, not a
	// citation, and demanding that it exist would make the guard object to the
	// documents explaining themselves - the same distinction align-tables.py
	// makes when a pipe inside a fence is data rather than a column.
	for _, m := range testCiteRe.FindAllString(outsideFences(doc), -1) {
		if !seen[m] {
			seen[m] = true

			out = append(out, m)
		}
	}

	sort.Strings(out)

	return out
}

// MissingTests reports cited test names that no test declares.
func MissingTests(where string, cited []string, declared map[string]bool) []string {
	var out []string

	for _, name := range cited {
		if declared[name] {
			continue
		}

		out = append(out, fmt.Sprintf(
			"%s cites %s, which no test declares", where, name))
	}

	sort.Strings(out)

	return out
}

// DeclaredTests finds every test a source file declares.
func DeclaredTests(source string) map[string]bool {
	out := map[string]bool{}

	for _, m := range testDeclRe.FindAllStringSubmatch(source, -1) {
		out[m[1]] = true
	}

	return out
}

// outsideFences blanks fenced code blocks, keeping line structure.
func outsideFences(doc string) string {
	var (
		out    strings.Builder
		inside bool
	)

	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inside = !inside

			out.WriteString("\n")

			continue
		}

		if inside {
			out.WriteString("\n")

			continue
		}

		out.WriteString(line)
		out.WriteString("\n")
	}

	return out.String()
}
