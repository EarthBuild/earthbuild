package ir_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// No test both changes ℋ and runs in parallel.
//
// **Because a comment was not enough.** SelectHashForTest says plainly that a
// test using it cannot be parallel - the choice is process-wide, and what it
// races against is every other test that hashes anything. The comment was
// written and then violated within the hour, by its author, and the symptom was
// four unrelated tests in another package failing in a way that looked like a
// regression in the code under test.
//
// The compiler cannot catch it and the race detector only catches it sometimes,
// because the two tests have to overlap. This is cheap and catches it always.
func TestNoParallelTestChangesTheHashFunction(t *testing.T) {
	t.Parallel()

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this package")
	}

	// The module root: this file is engine/ir/, so two levels up.
	root := filepath.Dir(filepath.Dir(filepath.Dir(here)))

	var offenders []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		// **A file that is not there is not an offender.** Other packages'
		// tests create and remove directories under this tree while this walks
		// it, and a walk that reported a vanished entry would fail for a reason
		// with nothing to do with what it guards - which is worse than not
		// guarding, because it cries wolf and gets disabled.
		if os.IsNotExist(err) {
			return nil
		}

		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err //nolint:wrapcheck // a walk's own error
		}

		f, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return nil // not ours to report; the package's own build says so
		}

		for _, decl := range f.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}

			var parallel, selects bool

			ast.Inspect(fn, func(n ast.Node) bool {
				sel, isSel := n.(*ast.SelectorExpr)
				if !isSel {
					return true
				}

				switch sel.Sel.Name {
				case "Parallel":
					parallel = true
				case "SelectHashForTest":
					selects = true
				}

				return true
			})

			if parallel && selects {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel+":"+fn.Name.Name)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, o := range offenders {
		t.Errorf("%s is parallel and changes ℋ"+
			"\n  the choice is process-wide, so it races every other test that"+
			"\n  hashes anything - and the failures land in whichever package"+
			"\n  happened to be hashing, looking like a regression there", o)
	}
}
