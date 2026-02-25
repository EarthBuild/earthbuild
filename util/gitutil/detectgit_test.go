package gitutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitRemoteURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		gitURL         string
		expectedGitURL string
		valid          bool
	}{
		{
			"github.com:user/repo",
			"github.com/user/repo",
			true,
		},
		{
			"git@github.com:user/repo.git",
			"github.com/user/repo",
			true,
		},
		{
			"git@gitlab.com:user/repo.git",
			"gitlab.com/user/repo",
			true,
		},
		{
			"ssh://git@github.com/EarthBuild/earthbuild.git",
			"github.com/EarthBuild/earthbuild",
			true,
		},
		{
			"https://git@github.com/EarthBuild/earthbuild.git",
			"github.com/EarthBuild/earthbuild",
			true,
		},
	}
	for _, test := range tests {
		gitURL, err := ParseGitRemoteURL(test.gitURL)
		if !test.valid {
			if err == nil {
				t.Errorf("expected error did not occur")
			}

			continue
		}

		NoError(t, err, "ParseGitRemoteURL failed")
		Equal(t, test.expectedGitURL, gitURL)
	}
}

func TestDetectGitContentHash(t *testing.T) {
	t.Parallel()

	// Create a temp git repo with a known commit.
	dir := t.TempDir()

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %s\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("git", "init")
	run("git", "checkout", "-b", "main")
	run("git", "config", "commit.gpgsign", "false")

	// Write a file and commit.
	NoError(t, os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644))
	run("git", "add", "hello.txt")
	run("git", "commit", "--no-verify", "-m", "first")

	ctx := context.Background()

	contentHash, err := detectGitContentHash(ctx, dir)
	NoError(t, err)

	// Tree hash for a single file "hello.txt" containing "hello\n" is deterministic.
	Equal(t, "aaa96ced2d9a1c8e72c56b253a0e2fe78393feb7", contentHash)

	// The tree hash should differ from the commit hash.
	commitHash := run("git", "rev-parse", "HEAD")
	True(t, contentHash != commitHash)

	// Amend the commit (different commit hash, same tree hash).
	run("git", "commit", "--no-verify", "--amend", "-m", "amended")
	newCommitHash := run("git", "rev-parse", "HEAD")
	True(t, commitHash != newCommitHash)

	contentHash2, err := detectGitContentHash(ctx, dir)
	NoError(t, err)
	Equal(t, contentHash, contentHash2) // tree hash unchanged
}
