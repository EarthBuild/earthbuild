package cli

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitRepo makes a real repository with one commit and returns its path and the
// commit's hash.
//
// A local repository is a complete test of everything except the network: git
// does not know or care that the URL is a path, so clone, revision selection,
// and the checkout are all exercised for real. Reaching github is the one part
// this cannot check, and is also the part that is not ours.
func gitRepo(t *testing.T, files map[string]string) (dir, head string) {
	t.Helper()

	_, err := osexec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}

	dir = t.TempDir()

	for name, body := range files {
		p := filepath.Join(dir, name)
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, args := range [][]string{
		{"init", "-q", "-b", testMainTarget},
		{"add", "-A"},
		{"commit", "-q", "--no-verify", "-m", "one"},
	} {
		out, err := git(t, dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	out, err := git(t, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	return dir, strings.TrimSpace(out)
}

// git runs one command in an environment of the test's own making.
//
// The isolation is the point and was learnt the hard way: without it the
// helper inherits the developer's global git configuration, and on a machine
// that signs commits with a hardware key `git commit` blocks for two minutes
// waiting for a touch nobody knew to give. A test that shells out to a tool
// with user-level configuration has to say which configuration it means.
func git(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := osexec.CommandContext(ctx, "git", //nolint:gosec // a fixed argv
		append([]string{
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null",
			"-c", "user.email=test@example.invalid", "-c", "user.name=Test",
		}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)

	out, err := cmd.CombinedOutput()

	return string(out), err
}

// A checkout lands the repository's files where it was asked to put them, for
// every way a revision can be written.
func TestGitCheckout(t *testing.T) {
	t.Parallel()

	repo, head := gitRepo(t, map[string]string{testEarthfile: "VERSION 0.8\n"})

	for _, tc := range []struct {
		name string
		rev  string
	}{
		{"the default branch", ""},
		{"a branch by name", testMainTarget},
		{"a commit hash", head},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dest := filepath.Join(t.TempDir(), "checkout")

			err := gitCheckout(context.Background(), "file://"+repo, tc.rev, dest)
			if err != nil {
				t.Fatal(err)
			}

			b, err := os.ReadFile(filepath.Join(dest, testEarthfile)) //nolint:gosec // a fixture this test wrote
			if err != nil {
				t.Fatalf("the checkout has no Earthfile: %v", err)
			}

			if !strings.HasPrefix(string(b), "VERSION") {
				t.Errorf("the checkout holds %q", b)
			}
		})
	}
}

// A revision that does not exist says so, naming it.
//
// A checkout that silently fell back to the default branch would build
// different code from the one named and report success.
func TestGitCheckoutRefusesAMissingRevision(t *testing.T) {
	t.Parallel()

	repo, _ := gitRepo(t, map[string]string{testEarthfile: "VERSION 0.8\n"})

	err := gitCheckout(context.Background(), "file://"+repo, "no-such-revision",
		filepath.Join(t.TempDir(), "checkout"))
	if err == nil {
		t.Fatal("a missing revision was checked out anyway")
	}

	if !strings.Contains(err.Error(), "no-such-revision") {
		t.Errorf("the error does not name the revision:\n%s", err)
	}
}

// The second reference to a repository at a revision reuses the first checkout.
func TestARepositoryIsClonedOnceAcrossBuilds(t *testing.T) {
	t.Parallel()

	repo, head := gitRepo(t, map[string]string{testEarthfile: "VERSION 0.8\n"})

	cacheDir := t.TempDir()
	fetch := gitRemotes(context.Background(), cacheDir, func(string) string { return "file://" + repo })

	first, err := fetch("example.test/org/repo", head)
	if err != nil {
		t.Fatal(err)
	}

	// A marker in the checkout survives only if the second call reuses it.
	marker := filepath.Join(first, ".reused")
	err = os.WriteFile(marker, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	second, err := fetch("example.test/org/repo", head)
	if err != nil {
		t.Fatal(err)
	}

	if second != first {
		t.Errorf("the second fetch went to %s, want %s", second, first)
	}

	_, err = os.Stat(marker)
	if err != nil {
		t.Error("the repository was cloned again instead of being reused")
	}
}

// An unpinned reference is not cached across builds.
//
// `github.com/org/repo+target` means whatever the default branch holds now.
// Caching it by name would pin it to whatever it held the first time this
// machine ever saw it, and no later build could move it - a build that is
// reproducible by accident and wrong on purpose.
func TestAnUnpinnedReferenceIsNotCached(t *testing.T) {
	t.Parallel()

	repo, _ := gitRepo(t, map[string]string{testEarthfile: "VERSION 0.8\n"})

	cacheDir := t.TempDir()
	fetch := gitRemotes(context.Background(), cacheDir, func(string) string { return "file://" + repo })

	dir, err := fetch("example.test/org/repo", "")
	if err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(dir, ".stale")
	err = os.WriteFile(marker, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = fetch("example.test/org/repo", "")
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(marker)
	if err == nil {
		t.Error("an unpinned reference was served from a previous checkout")
	}
}

// The fetcher refuses to touch anything outside its own cache.
//
// Defence in depth: the interpreter already rejects a reference that could
// escape, and this is the layer that would do the damage - it calls RemoveAll
// on the path it computes. A check here costs nothing and does not depend on
// the layer above staying correct.
func TestAFetchCannotEscapeTheCache(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()

	outside := filepath.Join(cacheDir, "outside.txt")
	err := os.WriteFile(outside, []byte("keep me"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	fetch := gitRemotes(context.Background(), filepath.Join(cacheDir, "cache"),
		func(string) string { return "file:///nonexistent" })

	for _, tc := range []struct{ repo, rev string }{
		{"github.com/../../..", testMainTarget},
		{"github.com/org/repo", "../../.."},
		{"github.com/org/repo", "../../../outside.txt"},
	} {
		t.Run(tc.repo+":"+tc.rev, func(t *testing.T) {
			t.Parallel()

			_, err := fetch(tc.repo, tc.rev)
			if err == nil {
				t.Error("a path outside the cache was accepted")
			}
		})
	}

	_, err = os.Stat(outside)
	if err != nil {
		t.Error("a file outside the cache was removed")
	}
}

// A revision that looks like an option is refused rather than handed to git.
//
// `git fetch origin --upload-pack=...` runs a command of the caller's choosing
// on this machine. The revision comes from an Earthfile, so this is remote code
// choosing a local command.
func TestARevisionCannotBeAnOption(t *testing.T) {
	t.Parallel()

	repo, _ := gitRepo(t, map[string]string{testEarthfile: "VERSION 0.8\n"})

	for _, rev := range []string{"--upload-pack=touch /tmp/pwned", "-x", "--exec=id"} {
		t.Run(rev, func(t *testing.T) {
			t.Parallel()

			err := gitCheckout(context.Background(), "file://"+repo, rev,
				filepath.Join(t.TempDir(), "checkout"))
			if err == nil {
				t.Fatal("a revision that is an option was passed to git")
			}

			if !strings.Contains(err.Error(), "revision") {
				t.Errorf("the refusal does not say what was wrong:\n%s", err)
			}
		})
	}
}

// GIT CLONE fetches a repository, and a pinned ref is fetched once.
//
// The same repository at two refs is two checkouts: serving one for the other
// would build code nobody asked for, so the ref is part of the key.
func TestGitClonerKeysOnUrlAndRef(t *testing.T) {
	t.Parallel()

	repo, head := gitRepo(t, map[string]string{"README.md": "the repo\n"})

	cacheDir := t.TempDir()
	clone := gitCloner(context.Background(), cacheDir)
	first, err := clone("file://"+repo, head)
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.ReadFile(filepath.Join(first, "README.md")) //nolint:gosec // a fixture this test wrote
	if err != nil {
		t.Fatalf("the checkout is missing its files: %v", err)
	}

	// A marker survives only if the second call reuses the checkout.
	marker := filepath.Join(first, ".reused")
	err = os.WriteFile(marker, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	again, err := clone("file://"+repo, head)
	if err != nil {
		t.Fatal(err)
	}

	if again != first {
		t.Errorf("one url and ref gave two directories")
	}

	_, err = os.Stat(marker)
	if err != nil {
		t.Error("a pinned checkout was fetched twice")
	}

	// A different ref is a different directory.
	other, err := clone("file://"+repo, testMainTarget)
	if err != nil {
		t.Fatal(err)
	}

	if other == first {
		t.Error("two refs share a checkout")
	}
}
