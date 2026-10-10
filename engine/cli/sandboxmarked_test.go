package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// The engine is marked as having a sandbox where the sandbox is made.
//
// **Because a caller that forgets leaves a VM running.** `close()` shuts the
// sandbox down only when `started` says there is one, and `started` was set by
// each caller that wanted a sandbox rather than by the thing that makes one.
// `executorFor` did not set it: a build that reached a sandbox through that
// path - and every build with a condition the interpreter cannot decide does -
// left its guest running and its store device claimed for the life of the
// process.
//
// One corpus run in one process was refused 26 times with `the store device is
// in use by this build itself: a sandbox it started has not been stopped`,
// after a first leak on the failure paths of Start had already been fixed. The
// flag has to be set where the machine is made, or the next caller forgets too.
func TestTheEngineIsMarkedWhereTheSandboxIsMade(t *testing.T) {
	t.Parallel()

	at := filepath.Join("conditions.go")

	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, at, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", at, err)
	}

	var found bool

	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "sandboxed" || fn.Body == nil {
			return true
		}

		ast.Inspect(fn.Body, func(in ast.Node) bool {
			as, ok := in.(*ast.AssignStmt)
			if !ok {
				return true
			}

			for _, lhs := range as.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "started" {
					found = true
				}
			}

			return true
		})

		return false
	})

	if !found {
		t.Error("sandboxed() does not mark the engine as having a sandbox" +
			"\n  close() skips a sandbox the engine is not marked as having," +
			" so every caller that obtains one and does not set the flag" +
			" leaves a guest running and its store device claimed" +
			"\n  set it where the sandbox is made, not at each call site")
	}
}
