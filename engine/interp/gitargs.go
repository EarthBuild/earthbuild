package interp

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// gitFacts is what a build context's git repository says about itself.
//
// **Empty where there is no repository, never absent.** Every one of these is
// documented as "an empty string if no git directory is detected", and an
// Earthfile that stamps a label with one gets an empty label rather than a
// failure - which is the behaviour a build outside a checkout needs, and the
// behaviour a tarball of a release has always had.
type gitFacts struct {
	hash       string
	shortHash  string
	tree       string
	branch     string
	tag        string
	commitTime string
	authorTime string
	authorName string
	authorMail string
	origin     string
	project    string
}

// gitCache holds one answer per directory, for this process.
//
// A build asks for these once per target and there may be many targets; the
// answer cannot change while a build runs, and running `git` per target would
// put four subprocesses on the path of every one of them.
var gitCache sync.Map // dir -> gitFacts

// gitFactsFor reads a context directory's git facts.
//
// Four invocations rather than eleven: one `git log` supplies everything about
// the commit through a format string, and the rest are one question each that
// `log` cannot answer. Bounded by a timeout, because a `git` that hangs - a
// credential prompt on a URL, a filesystem that has gone away - would otherwise
// hang the build before it has read a line of the Earthfile.
func gitFactsFor(dir string) gitFacts {
	if v, ok := gitCache.Load(dir); ok {
		facts, _ := v.(gitFacts)

		return facts
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var facts gitFacts

	// %H hash, %T tree, %ct committer time, %at author time, %an name, %ae mail.
	// One call, and the field order is the contract between this and the parse
	// below - a format string and a set of indices, kept adjacent so they cannot
	// drift apart.
	if out, ok := git(ctx, dir, "log", "-1", "--format=%H%n%T%n%ct%n%at%n%an%n%ae"); ok {
		f := strings.Split(out, "\n")
		if len(f) >= 6 {
			facts.hash, facts.tree = f[0], f[1]
			facts.commitTime, facts.authorTime = f[2], f[3]
			facts.authorName, facts.authorMail = f[4], f[5]
		}
	}

	if facts.hash == "" {
		// No commit, or not a repository. Everything else describes the commit,
		// so there is nothing left to ask.
		gitCache.Store(dir, facts)

		return facts
	}

	if len(facts.hash) >= 8 {
		facts.shortHash = facts.hash[:8]
	}

	if out, ok := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); ok && out != "HEAD" {
		// `HEAD` is what a detached checkout answers, and it is the name of no
		// branch: reporting it would have an Earthfile tag an image `HEAD`.
		facts.branch = out
	}

	// The first tag pointing at this commit, which is what the documentation
	// promises. `--points-at` rather than `describe`, because `describe` walks
	// backwards and would name a tag this commit does not carry.
	if out, ok := git(ctx, dir, "tag", "--points-at", "HEAD"); ok {
		facts.tag = strings.SplitN(out, "\n", 2)[0]
	}

	if out, ok := git(ctx, dir, "config", "--get", "remote.origin.url"); ok {
		facts.origin = out
		facts.project = projectFromURL(out)
	}

	gitCache.Store(dir, facts)

	return facts
}

// git runs one read-only question in a directory, or reports that it cannot.
//
// Failure is not an error here: a directory with no repository, a `git` that is
// not installed and a repository with no commits all mean the same thing to a
// caller - the fact is not available, and the documented value is empty.
func git(ctx context.Context, dir string, args ...string) (string, bool) {
	//nolint:gosec // a fixed program and arguments this package chose
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)

	out, err := cmd.Output()
	if err != nil {
		return "", false
	}

	text := strings.TrimSpace(string(out))

	return text, text != ""
}

// scrubbed is a remote URL with any credentials taken out.
//
// **Prefer this wherever the value is printed or saved.** A URL that carried a
// token is a token in the layer, and a layer is pushed to places the token was
// not meant for.
func scrubbed(url string) string {
	at := strings.LastIndex(url, "@")
	if at < 0 {
		return url
	}

	scheme := strings.Index(url, "://")
	if scheme < 0 {
		// `git@github.com:org/repo` - the `@` is the *user*, not a credential,
		// and removing it would produce a URL nobody can clone.
		return url
	}

	return url[:scheme+3] + url[at+1:]
}

// projectFromURL is the `org/repo` part of a remote URL.
func projectFromURL(url string) string {
	trimmed := strings.TrimSuffix(scrubbed(url), ".git")

	if i := strings.LastIndex(trimmed, ":"); i >= 0 && !strings.Contains(trimmed[i:], "/") {
		return ""
	}

	// Everything after the host, which is the last two path elements for the
	// hosts anybody uses and the whole tail for the ones that nest deeper.
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return ""
	}

	// `git@host:org/repo` splits with the host and org joined; take the tail.
	last := parts[len(parts)-1]
	first := parts[len(parts)-2]

	if i := strings.LastIndex(first, ":"); i >= 0 {
		first = first[i+1:]
	}

	return first + "/" + last
}
