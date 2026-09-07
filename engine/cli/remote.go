package cli

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/engine/exec"
	"github.com/EarthBuild/earthbuild/engine/interp"
)

// gitRemotes checks repositories out under the build cache.
//
// urlFor is a seam for tests, which point it at a repository on this machine.
// git treats a path and a URL alike, so everything but reaching the network is
// exercised for real - and reaching the network is the part that is not ours.
func gitRemotes(ctx context.Context, cacheDir string, urlFor func(repo string) string) interp.Remotes {
	return func(repo, rev string) (string, error) {
		root := filepath.Join(cacheDir, "remotes")

		dest := filepath.Join(root, filepath.FromSlash(repo), revDir(rev))

		// The path is removed and recreated below, so being sure it is inside
		// the cache is the difference between clearing a checkout and deleting
		// something else. The interpreter already rejects a reference that
		// could get here, and this does not depend on it having done so.
		if !within(root, dest) {
			return "", fmt.Errorf("%q at %q is not a place in the build cache", repo, rev)
		}

		// A pinned revision is immutable, so a checkout of it is too and is
		// reused. An unpinned one means "whatever that branch holds now", and
		// reusing it would pin the reference to whatever this machine happened
		// to see first - a build reproducible by accident and wrong on purpose.
		if rev != "" {
			_, err := os.Stat(filepath.Join(dest, ".git"))
			if err == nil {
				return dest, nil
			}
		}

		err := os.RemoveAll(dest)
		if err != nil {
			return "", fmt.Errorf("clear the previous checkout of %s: %w", repo, err)
		}

		err = gitCheckout(ctx, urlFor(repo), rev, dest)
		if err != nil {
			return "", err
		}

		return dest, nil
	}
}

// httpsURL is how a reference names a repository when nothing says otherwise.
func httpsURL(repo string) string { return "https://" + repo + ".git" }

// revDir names the directory a revision is checked out into. An unpinned
// reference has no revision, and its checkout is transient.
func revDir(rev string) string {
	if rev == "" {
		return "@default"
	}

	return rev
}

// gitCheckout puts a repository at a revision into dest.
//
// Fetched rather than cloned, because a revision may be a tag, a branch or a
// commit and only fetch takes all three: `clone --branch` refuses a hash. The
// depth is 1 - a build needs the tree, never the history - and the revision is
// named explicitly so a server that cannot supply it fails here rather than
// silently handing over its default branch, which would build different code
// from the one named and report success.
func gitCheckout(ctx context.Context, url, rev, dest string) error {
	err := os.MkdirAll(dest, 0o750)
	if err != nil {
		return fmt.Errorf("make room for the checkout: %w", err)
	}

	want := rev
	if want == "" {
		want = "HEAD"
	}

	// A revision beginning with a dash is an option, and `git fetch origin
	// --upload-pack=<cmd>` runs <cmd> on this machine. The revision comes from
	// an Earthfile, so that is remote text choosing a local command.
	if strings.HasPrefix(want, "-") {
		return fmt.Errorf("%q is not a revision: it would be read as an option to git", rev)
	}

	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", url},
		{"fetch", "-q", "--depth", "1", "origin", "--end-of-options", want},
		{"checkout", "-q", "FETCH_HEAD"},
	} {
		cmd := osexec.CommandContext(ctx, "git", args...) //nolint:gosec // a fixed argv
		cmd.Dir = dest

		// Nothing interactive: a build that stops for a credential prompt has
		// hung as far as anyone watching it can tell.
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=")

		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("fetch %s at %s: %w\n  %s",
				url, want, err, strings.TrimSpace(string(out)))
		}
	}

	return nil
}

// within reports whether path is inside root.
func within(root, path string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}

	p, err := filepath.Abs(path)
	if err != nil {
		return false
	}

	return p == r || strings.HasPrefix(p, r+string(os.PathSeparator))
}

// gitCloner fetches a repository named by GIT CLONE.
//
// Keyed on the url and ref together, because the same repository at two refs is
// two different checkouts and serving one for the other would build code nobody
// asked for. Unpinned - no `--branch` - is fetched afresh each time for the
// reason an unpinned image reference is: "whatever that repository holds now"
// cannot be answered from a directory written last week.
func gitCloner(ctx context.Context, cacheDir string) interp.GitClone {
	return func(url, ref string) (string, error) {
		key := exec.ImageCacheKey(url, ref)
		dest := filepath.Join(cacheDir, "clones", key)

		if ref != "" {
			_, err := os.Stat(filepath.Join(dest, ".git"))
			if err == nil {
				return dest, nil
			}
		}

		err := os.RemoveAll(dest)
		if err != nil {
			return "", fmt.Errorf("clear the previous checkout of %s: %w", url, err)
		}

		err = gitCheckout(ctx, url, ref, dest)
		if err != nil {
			return "", err
		}

		return dest, nil
	}
}
