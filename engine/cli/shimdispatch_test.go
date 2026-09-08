package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every re-exec entry point is dispatched by every binary the engine re-execs.
//
// **Because a binary that does not dispatch runs something else instead.** The
// engine gives a shim its own argv and re-executes `os.Executable()`. Under
// `go test` that is the test binary, which knew nothing about `vm-net` - so
// each microVM's network shim started the whole corpus gate again, recursively.
// It cost 88 test processes, 280 attempts at a 246-invocation corpus, store
// claims held by builds that were themselves the gate, and a parity number that
// measured nothing.
//
// Nothing in the failure said "recursion". It said `the store device is in use
// by another build`, which was true and useless.
func TestEveryShimIsDispatchedWhereverThisBinaryIsReExecuted(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	// The commands, read from where they are declared rather than listed here:
	// a list in a test is a list that the next shim is not added to.
	want := shimCommands(t, root)
	if len(want) < 2 {
		t.Fatalf("found %d re-exec commands, and the engine has at least two"+
			" (the agent and the network shim) - this guard is not looking"+
			" where they are declared", len(want))
	}

	for _, where := range []string{
		filepath.Join(root, "cmd", "earth", "main.go"),
		filepath.Join(root, "engine", "cli", "main_test.go"),
	} {
		b, err := os.ReadFile(where)
		if err != nil {
			t.Fatalf("%s: %v\n  every binary the engine may re-execute has to"+
				" dispatch the shims, and this is one of them", where, err)
		}

		for _, cmd := range want {
			if !strings.Contains(string(b), cmd) {
				t.Errorf("%s does not dispatch %s"+
					"\n  the engine re-executes this binary with that argv, and"+
					" a binary that does not recognise it does whatever it does"+
					" normally - for the test binary, that is running the tests"+
					" again, inside itself",
					filepath.Base(where), cmd)
			}
		}
	}
}

// shimCommands finds the exported constants that name a re-exec entry point.
//
// A constant whose name ends in `Command` and whose value is a bare word: that
// is the shape of every one of them, and reading them rather than listing them
// is what makes this guard notice the next.
func shimCommands(t *testing.T, root string) []string {
	t.Helper()

	var found []string

	for _, pkg := range []string{"engine/exec", "engine/guestd"} {
		dir := filepath.Join(root, pkg)

		fset := token.NewFileSet()

		pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}

		for _, p := range pkgs {
			for _, f := range p.Files {
				for _, d := range f.Decls {
					gen, ok := d.(*ast.GenDecl)
					if !ok || gen.Tok != token.CONST {
						continue
					}

					for _, spec := range gen.Specs {
						v, ok := spec.(*ast.ValueSpec)
						if !ok || len(v.Names) != 1 {
							continue
						}

						name := v.Names[0].Name
						if !ast.IsExported(name) || !strings.HasSuffix(name, "Command") {
							continue
						}

						qualified := filepath.Base(pkg) + "." + name
						if !contains(found, qualified) {
							found = append(found, qualified)
						}
					}
				}
			}
		}
	}

	return found
}

func contains(all []string, one string) bool {
	for _, got := range all {
		if got == one {
			return true
		}
	}

	return false
}

// repoRoot is the checkout this test is part of.
func repoRoot(t *testing.T) string {
	t.Helper()

	at, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	return at
}
