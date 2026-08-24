package ignore_test

import (
	"os"
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

	m := matcherFor(t, root)

	// `root` is this repository, found by walking up from the test's own
	// directory. Context-bound so a wedged git dies with the test (noctx), and
	// the argv is fixed apart from that root (gosec G204).
	out, err := exec.CommandContext(t.Context(), //nolint:gosec // our own checkout
		"git", "-C", root, "ls-files", "-z").Output()
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

	for rel := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
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

	m := matcherFor(t, repoRoot(t))

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

// matcherFor reads the repository's ignore file, or skips.
//
// **The file has to be looked for, not inferred from the matcher.** `Empty`
// reports whether there is a matcher, and `Read` always builds one - it seeds it
// with `Implicit`, so a checkout with no ignore file still yields a matcher that
// excludes the Earthfile. Skipping on `Empty` therefore never skipped, and these
// tests ran against the implicit patterns alone.
//
// Which is only visible where the file is absent, and the one place that
// reliably happens is inside a build context: `.earthlyignore` is implicitly
// excluded from every context, so `+unit-test` runs these tests against a copy
// of the repository that cannot contain the thing they are about. They failed
// there and nowhere else (E585).
func matcherFor(t *testing.T, root string) ignore.Matcher {
	t.Helper()

	_, err := os.Stat(filepath.Join(root, ".earthlyignore"))
	if err != nil {
		t.Skip("no .earthlyignore here: a build context never carries one")
	}

	m, err := ignore.Read(root)
	if err != nil {
		t.Fatalf("read the repository's own ignore file: %v", err)
	}

	return m
}
