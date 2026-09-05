package check_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/docs-internals/check"
)

// A comment where a test used to be says where the test went.
//
// A `_test.go` file that ends in a comment block with no declaration under it is
// a tombstone: somebody removed a test and kept its reasoning. That is a good
// habit - the reason a test went is worth more than the test was, and a deleted
// test with no note reads to the next person as coverage nobody thought about.
//
// It goes wrong when the note outlives what it describes. `engine/fleet`
// carried one recording that a fragment reaching the store is not checked
// against its layer - in the present tense, two increments after
// `Fragments.PutVerified` closed exactly that hole. **A gap written down as a
// test becomes a gap written down in a comment**, which is the thing that
// comment itself said was worse (E481).
//
// So the rule is the one the good tombstone already follows: name a test that
// exists. `saveimage_test.go` ends with "Pushing is recorded rather than
// refused; see TestSaveImagePushIsRecorded", and that pointer is what makes it
// checkable - the citation guard in this package then keeps the name honest, and
// a reader has somewhere to go.
func TestATombstoneNamesATestThatExists(t *testing.T) {
	t.Parallel()

	names := regexp.MustCompile(`\bTest[A-Za-z0-9_]+`)
	declared := declaredTests(t)

	for _, tomb := range tombstones(t) {
		found := names.FindAllString(tomb.text, -1)
		if len(found) == 0 {
			t.Errorf("%s:%d ends in a comment with no declaration under it and"+
				" names no test\n  %s\n  say which test carries this now, or"+
				" delete the comment with the test it described",
				tomb.file, tomb.line, firstLineOf(tomb.text))

			continue
		}

		// And the name has to resolve, or the pointer is the same nothing as
		// no pointer: this guard would otherwise be satisfied by a test that
		// was itself renamed, which is how the citation it replaces went
		// stale in the first place.
		for _, name := range found {
			if !declared[name] {
				t.Errorf("%s:%d points at %s, and no such test exists\n  %s",
					tomb.file, tomb.line, name, firstLineOf(tomb.text))
			}
		}
	}
}

// tombstone is a comment block after the last declaration in a test file.
type tombstone struct {
	file string
	text string
	// Last, so the two strings sit together (govet fieldalignment).
	line int
}

// tombstones finds them.
//
// Multi-line only: a single trailing `//nolint` or a one-line aside is not
// somebody's reasoning about a departed test, and treating it as one would make
// this guard noise.
func tombstones(t *testing.T) []tombstone {
	t.Helper()

	var out []tombstone

	err := filepath.WalkDir(repoRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not this guard's business
		}

		if d.IsDir() && (d.Name() == skipGit || d.Name() == skipModules || d.Name() == skipTestdata) {
			return filepath.SkipDir
		}

		if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()

		f, perr := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if perr != nil {
			return nil //nolint:nilerr // a file that does not parse is another test's news
		}

		var last token.Pos
		for _, decl := range f.Decls {
			if decl.End() > last {
				last = decl.End()
			}
		}

		for _, cg := range f.Comments {
			if cg.Pos() > last && len(cg.List) > 1 {
				out = append(out, tombstone{
					file: p,
					line: fset.Position(cg.Pos()).Line,
					text: cg.Text(),
				})
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// firstLineOf is the claim, for a diagnostic.
func firstLineOf(s string) string {
	if first, _, ok := strings.Cut(s, "\n"); ok {
		return first
	}

	return s
}

// declaredTests is every test in the tree, by name.
//
// The same scan `TestEveryCitedTestExists` runs over the same helper, because
// "does this name resolve" has one answer and two questions asking it.
func declaredTests(t *testing.T) map[string]bool {
	t.Helper()

	out := map[string]bool{}

	err := filepath.WalkDir(repoRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not this guard's business
		}

		if d.IsDir() && (d.Name() == skipGit || d.Name() == skipModules || d.Name() == skipTestdata) {
			return filepath.SkipDir
		}

		if d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return nil
		}

		// As above: a walk of this repository's own tree, in a test.
		b, readErr := os.ReadFile(filepath.Clean(p))
		if readErr != nil {
			return nil //nolint:nilerr // as above
		}

		for name := range check.DeclaredTests(string(b)) {
			out[name] = true
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(out) < 200 {
		t.Fatalf("only %d tests found in the tree, so the scan is wrong rather"+
			" than the source", len(out))
	}

	return out
}
