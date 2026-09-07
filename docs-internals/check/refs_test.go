package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/docs-internals/check"
)

// docRoot is this package's directory's parent: docs-internals.
const docRoot = ".."

// repoRoot is where the Go source lives.
const repoRoot = "../.."

// A citation in the code names a section that exists.
//
// The engine's comments cite the specification constantly - `§3.3` for what a
// layer records, `§4.7.3` for scheduling determinism, `A3` for confinement - and
// those citations are the reason the comments are worth reading: they say where
// the rule comes from rather than restating it.
//
// **They rot silently.** The green paper's own cross-references are checked
// mechanically for exactly this reason (the `green-paper` skill says so, and it
// has already caught two dangling ones); the code's citations *into* the paper
// were not checked at all.
//
// Nothing was found dangling, which is the good outcome and not the reason this
// exists. The reason is that measuring it by hand produced two wrong answers
// first:
//
//	heading extraction kept a trailing "."      §3 looked dangling against "3."
//	the pattern stopped at the first letter     §2a-bis was truncated to §2a
//
// Both made a resolving citation look broken; either could as easily have made
// a broken one look fine. A check performed by hand is a check performed
// differently each time.
func TestEveryCitationInTheCodeResolves(t *testing.T) {
	t.Parallel()

	paper := docOf(t, "green-paper.md")
	plan := docOf(t, "plan-native-engine.md")

	sections, plans := check.Sections(paper), check.Sections(plan)
	equations := check.Equations(paper)

	if len(sections) == 0 || len(plans) == 0 || len(equations) == 0 {
		t.Fatal("a document defines nothing, so every citation into it would fail")
	}

	for _, path := range goSources(t, repoRoot) {
		// This package quotes citations as examples of what the patterns must
		// match, so scanning itself finds them and reports them as dangling.
		// The exemption is the package rather than one file because the check
		// moved into `citations.go` and took its examples with it - a rule
		// scoped to a filename outlives the file it was about.
		//
		// A check that names its own subject matter has to skip itself or be
		// written so that it cannot describe itself, and the first is honest
		// where the second is a contortion.
		if strings.Contains(filepath.ToSlash(path), "docs-internals/check/") {
			continue
		}

		b, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatal(err)
		}

		for _, p := range check.CitationProblems(path, string(b), sections, plans, equations) {
			t.Error(p)
		}
	}
}

// And the extractors are checked, by giving them text whose answer is known.
//
// Both of E128's mistakes were *here* rather than in the comparison: a heading
// pattern that kept the trailing dot, and a citation pattern that stopped at
// the first letter. Both made a resolving citation look broken, and either
// could as easily have made a broken one look fine.
func TestTheExtractorsFindWhatTheyAreFor(t *testing.T) {
	t.Parallel()

	doc := "## 2. State\n### 3.3a Metadata\n### 2a-bis Observed inputs\n\n(4.10)  x\n"

	sections := check.Sections(doc)
	for _, want := range []string{"2", "3.3a", "2a-bis"} {
		if !sections[want] {
			t.Errorf("section %q was not found in %q", want, doc)
		}
	}

	if sections["2."] {
		t.Error("a heading's trailing dot survived, so a citation of §2 would not resolve")
	}

	if sections["2a"] {
		t.Error("a section name was truncated at its first letter")
	}

	if !check.Equations(doc)["4.10"] {
		t.Error("a numbered equation was not found")
	}
}

// And the comparison is checked, by citing things that are not there.
func TestTheCitationCheckNoticesWhatItIsFor(t *testing.T) {
	t.Parallel()

	paper := map[string]bool{"3.3": true}
	plan := map[string]bool{"2a-bis": true}
	eqs := map[string]bool{"4.10": true}

	for _, tc := range []struct{ name, src, want string }{
		{"a section that does not exist", "// see §9.9 for this", "§9.9"},
		{"an equation that does not exist", "// as (9.9) says", "(9.9)"},
		{"a plan reference checked against the plan", "// plan-native-engine.md §9z", "§9z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := check.CitationProblems("x.go", tc.src, paper, plan, eqs)
			if len(got) == 0 {
				t.Fatalf("no problem reported for %q", tc.src)
			}

			if !strings.Contains(strings.Join(got, "\n"), tc.want) {
				t.Errorf("the problem does not mention %q: %v", tc.want, got)
			}
		})
	}

	// And says nothing about citations that resolve.
	ok := "// §3.3 and (4.10) and plan-native-engine.md §2a-bis"
	if got := check.CitationProblems("x.go", ok, paper, plan, eqs); len(got) != 0 {
		t.Errorf("resolving citations were reported as problems: %v", got)
	}
}

// docOf reads a document beside this package.
func docOf(t *testing.T, name string) string {
	t.Helper()

	// The name comes from this test's own table, joined onto this
	// repository's docs directory. There is no caller to supply a path.
	b, err := os.ReadFile(filepath.Join(docRoot, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	return string(b)
}

// goSources is every .go file under root, excluding vendored trees.
func goSources(t *testing.T, root string) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			switch d.Name() {
			case skipGit, "vendor", skipModules, "build", skipTestdata:
				return filepath.SkipDir
			}

			return nil
		}

		if strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(out) == 0 {
		t.Fatal("no Go sources found, so this test asserts nothing")
	}

	return out
}

// The specification does not define one equation number twice.
func TestNoEquationNumberIsDefinedTwice(t *testing.T) {
	t.Parallel()

	for _, p := range check.DuplicateEquations(docOf(t, "green-paper.md")) {
		t.Error(p)
	}
}

// And the check notices, and does not mistake a citation for a definition.
//
// The extractor's third bug: `(4.2), so a scheduler may choose` begins a line
// and is a *reference*, and without requiring whitespace after the number the
// specification appeared to define `(4.2)` twice. Two of this package's three
// extractor bugs have now been "the pattern matched something that was not a
// definition" (E128, E144).
func TestTheDuplicateCheckKnowsADefinitionFromACitation(t *testing.T) {
	t.Parallel()

	twice := "(4.1)  x ≡ y\n(4.1)  x ≡ z\n"
	if got := check.DuplicateEquations(twice); len(got) == 0 {
		t.Error("two definitions of one number were not reported")
	}

	cited := "(4.1)  x ≡ y\n(4.1), which the scheduler may choose freely within\n"
	if got := check.DuplicateEquations(cited); len(got) != 0 {
		t.Errorf("a citation at the start of a line was counted as a definition: %v", got)
	}
}

// Every test-plan item the specification cites exists.
//
// §5.1 says what tests each invariant, and for two it answers with a test-plan
// item rather than a suite in the tree - the invariants whose enforcement is
// *planned*. The citation is then the only thing connecting the promise to the
// work, so a citation nobody checks is a promise nobody checks.
func TestEveryTestPlanCitationResolves(t *testing.T) {
	t.Parallel()

	items := check.TestPlanItems(docOf(t, "test-plan.md"))
	if len(items) == 0 {
		t.Fatal("the test plan defines no items, so every citation would fail")
	}

	for _, p := range check.TestPlanCitations(docOf(t, "green-paper.md"), items) {
		t.Error(p)
	}
}

// And the check notices a citation that names nothing.
func TestTheTestPlanCheckNoticesADanglingItem(t *testing.T) {
	t.Parallel()

	items := check.TestPlanItems("### a16. Capability gate test\n### c4. Crash safety\n")

	for _, want := range []string{"a16", "c4"} {
		if !items[want] {
			t.Errorf("item %q was not found", want)
		}
	}

	got := check.TestPlanCitations("tested by test-plan z9 and test-plan a16", items)
	if len(got) != 1 {
		t.Fatalf("expected one dangling citation, got %v", got)
	}

	if !strings.Contains(got[0], "z9") {
		t.Errorf("the wrong citation was reported: %v", got)
	}
}

// Every experiment the documents cite was run.
//
// An experiment is the only evidence the invariant table and the stage table
// have. A citation of one nobody ran is a claim with no evidence that reads
// exactly like a claim with evidence.
func TestEveryExperimentCitationResolves(t *testing.T) {
	t.Parallel()

	defined := check.Experiments(docOf(t, "experiments-adversarial.md"))
	if len(defined) < 100 {
		t.Fatalf("only %d experiments found, so the pattern is wrong rather than"+
			" the documents", len(defined))
	}

	for _, doc := range []string{"green-paper.md", "plan-native-engine.md", "test-plan.md"} {
		for _, p := range check.ExperimentCitations(doc, docOf(t, doc), defined) {
			t.Error(p)
		}
	}
}

// And the check knows a sub-section is a definition.
//
// E5b and E5c are `### E5b` under `## E5`, and a pattern anchored to `##`
// reported the invariant table as citing two experiments that do not exist -
// including the one testing I3. **Fourth pattern in this package to have been
// too narrow**, and the first where being too narrow would have condemned the
// documents rather than excused them.
func TestTheExperimentCheckAcceptsSubSections(t *testing.T) {
	t.Parallel()

	log := "## E5 - cache-hit parity\n### E5b - observed inputs\n### E5c - poison the cache\n"

	defined := check.Experiments(log)
	for _, want := range []string{"E5", "E5b", "E5c"} {
		if !defined[want] {
			t.Errorf("%s was not found in %q", want, log)
		}
	}

	if got := check.ExperimentCitations("x.md", "tested by E5b and E5c", defined); len(got) != 0 {
		t.Errorf("sub-section experiments were reported as missing: %v", got)
	}

	if got := check.ExperimentCitations("x.md", "tested by E999", defined); len(got) != 1 {
		t.Errorf("an experiment nobody ran was not reported: %v", got)
	}
}

// A test's own output is not an equation reference.
//
// `--- FAIL: TestX (0.00s)` is what `go test` prints, and a fixture containing
// it is ordinary. The citation pattern read `(0.00s)` as a reference to
// equation 0.00s and reported the file as citing something the green paper does
// not have - a guard accusing source that was entirely correct.
//
// Equations are numbered from one, so a fractional part beginning with zero is
// not one. Narrower on purpose: the previous four corrections in this package
// widened a pattern that missed things, and this is the other failure, which is
// worse for a guard - a check that misses goes unnoticed, a check that accuses
// gets deleted.
func TestATestTimingIsNotAnEquation(t *testing.T) {
	t.Parallel()

	var (
		paper = map[string]bool{"1": true}
		plan  = map[string]bool{}
		eqs   = map[string]bool{"1.1": true}
	)

	for _, bad := range []string{
		"--- FAIL: TestX (0.00s)",
		"--- PASS: TestY (12.34s)",
		"ok  pkg 0.006s",
	} {
		if got := check.CitationProblems("x_test.go", bad, paper, plan, eqs); len(got) != 0 {
			t.Errorf("%q was read as a citation: %v", bad, got)
		}
	}

	// And a real one still resolves, or fails to.
	if got := check.CitationProblems("x.go", "see (1.1)", paper, plan, eqs); len(got) != 0 {
		t.Errorf("a real equation reference was reported as dangling: %v", got)
	}

	if got := check.CitationProblems("x.go", "see (9.9)", paper, plan, eqs); len(got) != 1 {
		t.Errorf("an equation that does not exist was not reported: %v", got)
	}
}

// Every test the documents cite as evidence exists.
//
// A stage's state, an invariant's row and a milestone's proof are all written as
// "see TestSomething", and that test is usually the *only* evidence for the
// sentence around it. Tests get renamed; a citation does not. A name nothing
// declares is a claim with nothing behind it that reads exactly like one with
// something behind it - E149's argument about experiments, applied to the other
// kind of evidence these documents lean on.
func TestEveryCitedTestExists(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{}

	err := filepath.Walk("../..", func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not this test's problem
		}

		if fi.IsDir() && (fi.Name() == skipGit || fi.Name() == skipModules || fi.Name() == skipTestdata) {
			return filepath.SkipDir
		}

		if fi.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return nil
		}

		// `p` is what the walk of this repository handed us (G122): a walk of a
		// checkout, in a test, with no untrusted input anywhere near it.
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			return nil //nolint:nilerr // as above
		}

		for name := range check.DeclaredTests(string(b)) {
			declared[name] = true
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(declared) < 200 {
		t.Fatalf("only %d tests found in the tree, so the scan is wrong rather"+
			" than the documents", len(declared))
	}

	// Only the documents that speak in the present tense.
	//
	// The first version of this checked all four and found four "dangling"
	// citations, every one of them correct:
	//
	//   - test-plan.md describes tests **to be written** - `TestCapabilityGate`
	//     is a specification, not a reference - and one seed test that lives in
	//     the buildkit fork rather than here;
	//   - experiments-adversarial.md is a record of what happened, and names two
	//     tests under the heading "two tests that had to go rather than stay".
	//     A log that could only mention tests which still exist would be a log
	//     that rewrites itself.
	//
	// The green paper and the plan assert what *is*, so a name in them has to
	// resolve. Scoping the guard to them is the distinction those documents
	// already make, not a way round an inconvenient failure.
	for _, doc := range []string{"green-paper.md", "plan-native-engine.md"} {
		for _, p := range check.MissingTests(doc, check.TestNames(docOf(t, doc)), declared) {
			t.Error(p)
		}
	}
}
