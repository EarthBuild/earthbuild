package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No worker is built without the executor that runs its steps.
//
// localWorker learns what this machine emulates or translates only by asking
// the executor, so `nil` reads the host's binfmt register - which a Mac does not
// have. The conditions engine built its worker that way, and a LET or IF that
// needed an amd64 target was told no worker could run it, on a Mac whose
// sandbox runs amd64 through Rosetta; the build path, which passed the executor,
// placed the same step fine.
func TestEveryLocalWorkerIsGivenItsExecutor(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		src, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}

		f, parseErr := parser.ParseFile(fset, name, src, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}

			if fn, isIdent := call.Fun.(*ast.Ident); !isIdent || fn.Name != "localWorker" {
				return true
			}

			if arg, isIdent := call.Args[1].(*ast.Ident); isIdent && arg.Name == "nil" {
				t.Errorf("%s: localWorker is given no executor, so it cannot say what this"+
					" machine emulates or translates", fset.Position(call.Pos()))
			}

			return true
		})
	}
}
