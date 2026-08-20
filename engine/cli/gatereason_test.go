package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// darwinOnly are the symbols that genuinely do not exist off macOS.
//
// `exec.NewApple` is the constructor for the VM backend and is in a
// `_darwin.go` file; the rest are the shell out to Apple's CLI. A test naming
// one of these has a reason to be `//go:build darwin`. A test naming none of
// them has a tag and no reason for it.
var darwinOnly = []string{"NewApple", "exec.Apple", "container system", "vmnet"}

// goIgnores is the go tool's own rule for a file that is not part of a package.
//
// Names beginning `.` or `_` are ignored by the build system - `go help
// packages` - so nothing was compiled from them and no source guard has anything
// to say about them.
func goIgnores(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// A test is gated to one platform only when it names something that is.
//
// E106 found the cross-backend suite built for darwin alone, so the shared case
// table had never run against the Linux backend. Its cause was one line -
// `exec.NewApple().Available()` as a skip guard - and eighteen sibling files in
// this package have the same line, or in three cases no darwin-specific content
// whatsoever: `corpusclass_darwin_test.go` imports `testing` and nothing else.
//
// The guard has a portable spelling now, `requireSandbox(t)`, so naming
// `NewApple` in a test is a choice rather than a necessity. This test says so.
//
// **What a build tag costs.** It is not a skip. A skipped test appears in the
// output as SKIP and somebody eventually asks why; a file excluded by a build
// constraint is not compiled, is not counted, and appears nowhere at all. The
// suite reports `ok` and the number of tests that ran is the number somebody
// remembered to make portable. That is why this is a source guard and not a
// runtime one - at run time there is nothing left to ask.
func TestNoTestIsGatedToAPlatformWithoutNamingOne(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "_darwin_test.go") || goIgnores(name) {
			continue
		}

		// The file that *provides* the portable spelling. It names `NewApple`
		// because somebody has to, and it is nine lines with a sibling for each
		// other platform - which is the shape being asked for, not the one
		// being complained about.
		if name == "sandboxready_darwin_test.go" {
			continue
		}

		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatal(err)
		}

		body := string(b)

		var named []string

		for _, sym := range darwinOnly {
			if strings.Contains(body, sym) {
				named = append(named, sym)
			}
		}

		if len(named) == 0 {
			t.Errorf("%s is built for darwin only and names nothing darwin-specific:"+
				"\n  a build tag is not a skip - the file is not compiled, not counted,"+
				" and reports nothing"+
				"\n  drop the tag, or say in it which platform's behaviour is under test", name)

			continue
		}

		// Naming exactly the guard is the E106 shape: the test is portable and
		// the way it asks whether a sandbox exists is not.
		if len(named) == 1 && named[0] == "NewApple" &&
			strings.Contains(body, "apple container backend unavailable") {
			t.Errorf("%s is built for darwin only because of its skip guard:"+
				"\n  `exec.NewApple().Available()` is the only darwin-specific thing in it"+
				"\n  `requireSandbox(t)` asks the same question on either platform (E106)", name)
		}
	}
}

// The guard reads only what the compiler reads.
//
// A macOS `tar` carries extended attributes as AppleDouble members, and GNU tar
// on the far side materialises them as `._name` files. One landed beside
// `sandboxready_darwin_test.go`, the exemption above is an exact-name match, and
// the guard duly accused a file that is not source of being untagged source.
//
// **The go tool ignores any file beginning `.` or `_`** - it is not compiled,
// not vetted, not part of the package (see `go help packages`). A source guard
// that inspects what the compiler does not can only ever accuse: whatever it
// finds there, nothing was built from it.
//
// Third time in this line that the probe was the broken thing, and the second
// false red. Hence a rule spelled once and tested, rather than an exemption
// added per accident.
func TestTheGuardIgnoresWhatTheCompilerIgnores(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name   string
		ignore bool
	}{
		{"gatereason_test.go", false},
		{"sandboxready_darwin_test.go", false},
		{"._sandboxready_darwin_test.go", true},
		{"_scratch_darwin_test.go", true},
		{".hidden_darwin_test.go", true},
	} {
		if got := goIgnores(c.name); got != c.ignore {
			t.Errorf("goIgnores(%q) = %v, want %v", c.name, got, c.ignore)
		}
	}
}
