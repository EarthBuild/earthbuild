package cli

import (
	"context"
	"os"
	osexec "os/exec"
	"strings"
	"testing"
)

// GIT CLONE cannot read a repository on this machine.
//
// The URL is the Earthfile's and the clone runs here, so `file:///home/u/private`
// - or a bare path, which git also takes as a local clone - copies a repository
// the user can read into a build that may send it anywhere. BuildKit clones
// inside its daemon, where no such path exists.
//
// Refused by git itself, and the test says so: a refusal for any other reason
// (no git, a bad fixture) would pass for the wrong one.
func TestAGitCloneCannotReadALocalRepository(t *testing.T) {
	t.Parallel()

	_, err := osexec.LookPath("git")
	if err != nil {
		t.Skip("needs git on PATH")
	}

	repo := t.TempDir()

	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty", "-m", "private"},
	} {
		cmd := osexec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = repo
		// The fixture's commit, not the developer's: their global config may
		// sign commits, and a signing prompt hangs the test.
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")

		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("git %v: %v\n%s", args, runErr, out)
		}
	}

	clone := gitCloner(context.Background(), t.TempDir())

	for _, url := range []string{"file://" + repo, repo} {
		_, err := clone(url, "")
		if err == nil {
			t.Errorf("GIT CLONE %s cloned a repository on this machine", url)

			continue
		}

		if !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("GIT CLONE %s failed, but not because the transport was refused: %v", url, err)
		}
	}
}
