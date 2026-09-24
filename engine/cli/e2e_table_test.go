package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// minimumCases is a ratchet: the differential may grow, never shrink.
//
// It exists because of a real failure rather than a hypothetical one. An edit
// meant to add five cases silently matched nothing and added none; the suite
// stayed green, reported success, and the coverage that was believed to exist
// did not. A green run is not evidence of how much ran, and nothing else in a
// table-driven test notices a table that quietly got smaller.
//
// Raise it when cases are added. Lowering it is a deliberate act that says
// coverage was given up, which is a thing to argue for in a review rather than
// discover afterwards.
const minimumCases = 19

// TestTheCaseTableIsWellFormed checks the differential's table before either
// backend runs it.
//
// A malformed case does not fail loudly - it fails as a build that passes while
// asserting nothing, which is the most expensive kind of green.
func TestTheCaseTableIsWellFormed(t *testing.T) {
	t.Parallel()

	all := cases(t)

	if len(all) < minimumCases {
		t.Errorf("the table has %d cases, and %d were expected: coverage went backwards, "+
			"or an edit meant to add cases did not take", len(all), minimumCases)
	}

	// Duplicate names are a property of the *table*, so they are checked here
	// rather than inside the subtests. They were checked there, against a map
	// the subtests shared - which was correct while the subtests ran one at a
	// time and a data race the moment they did not:
	//
	//	WARNING: DATA RACE ... runtime.mapdelete_fast64()
	//
	// Go silently uniquifies duplicate subtest names, so a copied case that was
	// never edited runs twice and looks like two.
	seen := make(map[string]bool, len(all))

	for _, tc := range all {
		if seen[tc.name] {
			t.Errorf("two cases share the name %q", tc.name)
		}

		seen[tc.name] = true
	}

	for _, tc := range all {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.file == "" || tc.want == "" {
				t.Fatal("a case with no file or no expectation asserts nothing")
			}

			// The case must plausibly write the file it then reads, or the
			// assertion is about a file no step ever touched - which fails, but
			// for a reason that sends the reader to the engine rather than here.
			whole := tc.body + functionBlock
			if !strings.Contains(whole, "FILE") && !strings.Contains(whole, filepath.Base(tc.file)) {
				t.Errorf("nothing in the case writes %s", tc.file)
			}

			// A shared case must mean the same thing on both backends, and an
			// absolute path does not: `/script` is the image root in a sandbox
			// and this machine's root on a host. Catching it here explains the
			// rule; catching it in the runner is a permission error thirty
			// seconds into a container.
			if abs := absolutePaths(tc.body); len(abs) > 0 && !tc.sandboxOnly {
				t.Errorf("names the absolute path(s) %v, so it cannot be shared with the "+
					"host backend - mark it sandboxOnly, or make the path relative", abs)
			}
		})
	}
}

// absolutePaths finds tokens that name a path from the root.
//
// Deliberately crude: it over-reports rather than under-reports, because the
// consequence of a miss is a case that writes to a developer's root filesystem
// and the consequence of a false positive is one word in a table.
func absolutePaths(body string) []string {
	var found []string

	for line := range strings.SplitSeq(body, "\n") {
		for _, tok := range strings.FieldsFunc(line, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '"' || r == '\'' || r == ';' || r == '>'
		}) {
			// A double slash is a comment or a URL, not a path from the root.
			if strings.HasPrefix(tok, "/") && !strings.HasPrefix(tok, "//") {
				found = append(found, tok)
			}
		}
	}

	return found
}
