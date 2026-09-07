//go:build linux

package exec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// settingsHostOnly are guestd settings that deliberately do not cross into a
// guest, with the reason. Anything not listed has to cross.
var settingsHostOnly = map[string]string{}

// Every setting the agent reads reaches a guest that cannot read the host's
// environment.
//
// **A guest's environment comes from its kernel command line.** The process
// that starts a microVM does not hand its environment to the guest, so a
// setting the agent reads and `guestSettings` omits is silently ignored inside
// the VM - the code is there, the setting parses, and nothing happens.
//
// That is not hypothetical. Fifteen settings were absent from this list at
// once, which made an A/B of one of them produce identical numbers twice and
// look like a finding. Nothing said so, because there is nothing to say: the
// guest simply never hears.
//
// Parsed from the agent's own source, so adding a setting there and forgetting
// this list is a failure rather than a silence.
func TestEverySettingTheAgentReadsReachesTheGuest(t *testing.T) {
	t.Parallel()

	crossing := strings.Join(guestSettingNames(t), " ")

	for _, name := range envConstsIn(t, "../guestd") {
		if why, ok := settingsHostOnly[name]; ok {
			if strings.Contains(crossing, name) {
				t.Errorf("guestd.%s is listed as host-only (%s) and crosses anyway", name, why)
			}

			continue
		}

		if !strings.Contains(crossing, name) {
			t.Errorf("guestd.%s is read by the agent and does not cross into a guest"+
				"\n  add it to guestSettings, or to settingsHostOnly with a reason"+
				"\n  a guest reads its environment from the kernel command line and"+
				" nowhere else, so an omitted setting is silently ignored", name)
		}
	}
}

// guestSettingNames is the identifiers guestSettings lists.
func guestSettingNames(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "usernet_linux.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "guestSettings" {
			return true
		}

		ast.Inspect(fn, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				out = append(out, sel.Sel.Name)
			}

			return true
		})

		return false
	})

	if len(out) == 0 {
		t.Fatal("guestSettings lists nothing, which cannot be right")
	}

	return out
}

// envConstsIn is the exported Env* constants a package declares, by identifier.
//
// By name rather than by value, because the list in guestSettings names them
// the same way and a value would only match after both were resolved.
func envConstsIn(t *testing.T, dir string) []string {
	t.Helper()

	fset := token.NewFileSet()

	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				spec, ok := n.(*ast.ValueSpec)
				if !ok {
					return true
				}

				for i, id := range spec.Names {
					if !strings.HasPrefix(id.Name, "Env") || !id.IsExported() {
						continue
					}

					// A string constant, so a helper called EnvSomething is not
					// mistaken for a setting.
					if i < len(spec.Values) {
						if lit, ok := spec.Values[i].(*ast.BasicLit); !ok || lit.Kind != token.STRING {
							continue
						}
					}

					out = append(out, id.Name)
				}

				return true
			})
		}
	}

	if len(out) == 0 {
		t.Fatal("the agent declares no Env constants, which cannot be right")
	}

	_ = strconv.Itoa

	return out
}
