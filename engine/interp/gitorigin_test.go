package interp

import (
	"os/exec"
	"testing"
)

// TestARepositoryWithNoCommitStillHasAnOrigin.
//
// **`remote.origin.url` describes the repository, not the commit**, and the
// gathering returned early for a repository with no commit on the grounds that
// "everything else describes the commit". So a freshly-initialised checkout with
// a remote produced no qualifier, and `EARTHLY_TARGET` came out `+t` where
// `tests/empty-git.earth+test-origin-no-hash` asserts
// `github.com/earthly/earthly+t`.
//
// The hash is still empty, which the same corpus target asserts: nothing here
// invents a commit that does not exist.
func TestARepositoryWithNoCommitStillHasAnOrigin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	run(t, dir, "init")
	run(t, dir, "remote", "add", "origin", "https://github.com/earthly/earthly.git")

	got := gitFactsFor(dir)

	if got.hash != "" {
		t.Errorf("a repository with no commit reports the hash %q", got.hash)
	}

	for _, c := range []struct{ name, got, want string }{
		{"origin", got.origin, "https://github.com/earthly/earthly.git"},
		{"project", got.project, "earthly/earthly"},
		{"qualifier", got.qualifier, "github.com/earthly/earthly"},
	} {
		if c.got != c.want {
			t.Errorf("%s is %q, want %q", c.name, c.got, c.want)
		}
	}
}

// And a repository with no remote has nothing to qualify with - which is the
// other half of the same corpus file, asserting a bare `+test-empty`.
func TestARepositoryWithNoRemoteHasNoQualifier(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	run(t, dir, "init")

	got := gitFactsFor(dir)

	if got.qualifier != "" || got.origin != "" {
		t.Errorf("a repository with no remote reports origin %q, qualifier %q",
			got.origin, got.qualifier)
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
