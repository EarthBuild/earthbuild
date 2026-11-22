package exec

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A symlinked binary finds the agent beside the binary itself.
//
// **The ordinary way a developer installs one build of a tool.** `ln -s
// ~/src/build/earth ~/bin/arth` puts it on PATH without copying, so a rebuild is
// live immediately - and `os.Executable()` on macOS answers with the *link*, not
// what it points at. `filepath.Dir` of that is `~/bin`, where an agent built
// into the checkout is not, and the build fails with advice that tells you to
// put it somewhere it already is.
//
// Both directories are candidates because either may be the right one: an agent
// dropped beside the link is as deliberate as one built beside the binary, and
// asking twice costs one stat.
func TestASymlinkedBinaryFindsItsAgent(t *testing.T) {
	t.Parallel()

	// Resolved up front: on macOS `t.TempDir()` hands back a path under `/var`,
	// which is a symlink to `/private/var`, and what comes back is the resolved
	// one. Comparing them raw fails for a reason unrelated to what is tested.
	built := realDir(t, t.TempDir())
	onPath := realDir(t, t.TempDir())

	exe := filepath.Join(built, "earth")
	if err := os.WriteFile(exe, []byte("#!/bin/true\n"), 0o700); err != nil { //nolint:gosec // a fixture
		t.Fatal(err)
	}

	link := filepath.Join(onPath, "arth")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}

	got := besideExecutable(link)

	if !slices.Contains(got, built) {
		t.Errorf("looked in %v\n  want the directory the link points at (%s),"+
			" which is where a checkout's agent is built", got, built)
	}

	if !slices.Contains(got, onPath) {
		t.Errorf("looked in %v\n  want the link's own directory (%s) as well:"+
			" an agent put beside the link is as deliberate as one beside the"+
			" binary", got, onPath)
	}
}

// An ordinary binary looks beside itself, once.
//
// Two identical candidates would stat the same directory twice and, worse, print
// the same path twice in a diagnosis - which reads as a bug in the tool telling
// you about a bug in your setup.
func TestAnOrdinaryBinaryLooksBesideItselfOnce(t *testing.T) {
	t.Parallel()

	dir := realDir(t, t.TempDir())

	exe := filepath.Join(dir, "earth")
	if err := os.WriteFile(exe, []byte("#!/bin/true\n"), 0o700); err != nil { //nolint:gosec // a fixture
		t.Fatal(err)
	}

	if got := besideExecutable(exe); len(got) != 1 || got[0] != dir {
		t.Errorf("looked in %v, want exactly [%s]", got, dir)
	}
}

// A path that resolves to nothing still yields its own directory.
func TestABrokenLinkStillOffersItsOwnDirectory(t *testing.T) {
	t.Parallel()

	dir := realDir(t, t.TempDir())
	link := filepath.Join(dir, "arth")

	if err := os.Symlink(filepath.Join(dir, "gone"), link); err != nil {
		t.Fatal(err)
	}

	if got := besideExecutable(link); !slices.Contains(got, dir) {
		t.Errorf("looked in %v, want at least [%s]", got, dir)
	}
}

// realDir is a directory with every symlink resolved.
func realDir(t *testing.T, at string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(at)
	if err != nil {
		return at
	}

	return resolved
}
