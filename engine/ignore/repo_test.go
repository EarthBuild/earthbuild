package ignore_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/ignore"
)

// repoRoot is this repository, two directories up from engine/ignore.
func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	return root
}

// **No tracked file may be excluded from the build context.**
//
// The rule the ignore file states about itself is that it holds generated
// content, "every line of it gitignored and none of it part of the repository".
// This is that sentence, mechanically.
//
// It exists because the obvious generalisation is wrong in a way nothing else
// catches. Excluding build output suggests `**/dist`, and `examples/js/dist` and
// `tests/remote-cache/test2/dist` are *tracked* - so the pattern would drop
// source from every build that copies them, silently, and the cache would agree
// with itself all the way to a wrong artifact.
func TestNoTrackedFileIsExcludedFromTheContext(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	m, err := ignore.Read(root)
	if err != nil {
		t.Fatalf("read the repository's own ignore file: %v", err)
	}

	if m.Empty() {
		t.Skip("this checkout has no ignore file to check")
	}

	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("no git here: %v", err)
	}

	// The build's own description is tracked and is deliberately not one of its
	// inputs, so the implicit set is the one exception to the rule. See
	// ignore.Implicit.
	implicit := make(map[string]bool, len(ignore.Implicit))
	for _, name := range ignore.Implicit {
		implicit[strings.TrimSuffix(name, "/")] = true
	}

	var dropped []string

	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel == "" || implicit[filepath.Base(rel)] {
			continue
		}

		if m.Excludes(rel) {
			dropped = append(dropped, rel)
		}
	}

	if len(dropped) > 0 {
		t.Errorf("%d tracked files are excluded from the build context, starting %v"+
			"\n  a pattern in .earthlyignore is matching source rather than build output",
			len(dropped), dropped[:min(5, len(dropped))])
	}
}

// The other half: the generated trees the ignore file exists to keep out are
// actually kept out. Without this, deleting every pattern would leave the test
// above green - a guard that only forbids is one nobody notices switching off.
func TestGeneratedTreesAreExcludedFromTheContext(t *testing.T) {
	t.Parallel()

	m, err := ignore.Read(repoRoot(t))
	if err != nil {
		t.Fatalf("read the repository's own ignore file: %v", err)
	}

	if m.Empty() {
		t.Skip("this checkout has no ignore file to check")
	}

	for _, rel := range []string{
		"examples/next-js/.next/cache/webpack/client-production/0.pack",
		"examples/go/build/go-example",
		"examples/readme/go1/build/go-example",
		"examples/typescript-node/node_modules/x/index.js",
		"engine/store/testdata/bigtree-20000/d63/e55/f",
		"build/linux/arm64/earthly",
	} {
		if !m.Excludes(rel) {
			t.Errorf("%s is hashed into the context and is generated content", rel)
		}
	}
}
