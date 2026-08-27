package interp

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Remotes checks a repository out and returns the directory it landed in.
//
// `repo` is the repository as written - `github.com/org/repo` - and `ref` is
// the revision after the colon, empty when none was given. What that revision
// means is the fetcher's business: a tag, a branch and a commit are all written
// the same way and only the thing doing the cloning can tell them apart.
//
// The seam keeps the network out of the interpreter. Which repository, which
// revision, which directory within it and how many times it is fetched are all
// decisions worth testing on every change; whether git can reach github is not,
// and testing them together tests neither.
type Remotes func(repo, ref string) (dir string, err error)

// WithRemotes supplies the fetcher for references to other repositories.
//
// Without one they are refused by name. That is what a plan-only caller needs:
// producing a graph must not clone a repository, and `earthbuild plan` running
// arbitrary `git` against the network to answer a question about a file would
// be a surprise in both directions.
func WithRemotes(fn Remotes) Option {
	return func(o *options) { o.remotes = fn }
}

// remote is a reference to a target in another repository.
type remote struct {
	// repo is `host/org/repo` - the first three path elements.
	repo string
	// rev is the revision after the colon, empty when unpinned.
	rev string
	// subdir is the path within the checkout, empty for its root.
	subdir string
}

// String rebuilds the reference as it was written, for diagnostics.
func (r remote) String() string {
	s := r.repo
	if r.subdir != "" {
		s += "/" + r.subdir
	}

	if r.rev != "" {
		s += ":" + r.rev
	}

	return s
}

// parseRemote reads `host/org/repo[/subdir][:rev]`.
//
// The revision sits at the end, immediately before the `+`, which is where
// Earthfiles write it. The repository is the first three elements because that
// is what a repository is on every host this syntax is used with; anything
// further along is a directory inside the checkout.
func parseRemote(path, where string) (remote, error) {
	var r remote

	if i := strings.LastIndex(path, ":"); i >= 0 {
		r.rev = path[i+1:]
		path = path[:i]

		if r.rev == "" {
			return remote{}, fmt.Errorf("%q names no revision after the colon (%s)", path, where)
		}
	}

	// The revision becomes a directory name and a git argument, and it arrives
	// from an Earthfile - which may itself have come from a repository this
	// build has just cloned. A `..` in it is a path outside the cache; a
	// leading `-` is an argument to git rather than a revision, and
	// `--upload-pack=` is git running a command of the Earthfile's choosing on
	// this machine.
	if r.rev != "" && !safeComponent(r.rev) {
		return remote{}, fmt.Errorf(
			"%q is not a revision (%s)"+
				"\n  a revision is a tag, a branch or a commit - not a path or an option",
			r.rev, where)
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	for _, part := range parts {
		if !safeComponent(part) {
			return remote{}, fmt.Errorf(
				"%q is not a repository path (%s)"+
					"\n  %q is not a name this can follow",
				path, where, part)
		}
	}

	if len(parts) < 3 {
		return remote{}, fmt.Errorf(
			"%q is not a repository (%s)"+
				"\n  a remote reference is host/org/repo[/path][:revision]+target",
			path, where)
	}

	r.repo = strings.Join(parts[:3], "/")
	r.subdir = strings.Join(parts[3:], "/")

	return r, nil
}

// fetchRemote checks the repository out, once per revision.
//
// Memoised because a fetch is a clone: doing it per reference turns a file that
// mentions a dependency three times into three clones. Keyed on repository
// *and* revision, because two revisions are two different sets of code and
// collapsing them would build one while reporting the other.
func (p *Plan) fetchRemote(r remote, where string) (string, error) {
	if p.opt.remotes == nil {
		return "", fmt.Errorf(
			"%q refers to a target in a remote repository (%s)"+
				"\n  the native engine builds Earthfiles on this machine"+
				"\n  to build this now, use --engine=buildkit",
			r.String(), where)
	}

	key := r.repo + ":" + r.rev
	if dir, ok := p.fetched[key]; ok {
		return dir, nil
	}

	dir, err := p.opt.remotes(r.repo, r.rev)
	if err != nil {
		return "", fmt.Errorf("fetch %s (%s): %w", r.String(), where, err)
	}

	if p.fetched == nil {
		p.fetched = map[string]string{}
	}

	p.fetched[key] = dir

	return dir, nil
}

// dirFor resolves a remote reference to the directory holding its Earthfile.
func (p *Plan) dirFor(r remote, where string) (string, error) {
	dir, err := p.fetchRemote(r, where)
	if err != nil {
		return "", err
	}

	return dir, nil
}

// safeComponent reports whether a path element can be used as written.
//
// One rule for both halves of a reference, because both end up as directory
// names under the build cache and as arguments to git. `.` and `..` walk out of
// the cache - which is then removed and recreated - and a leading `-` is read
// by git as an option rather than a name.
func safeComponent(s string) bool {
	if s == "" || s == "." || s == ".." || strings.HasPrefix(s, "-") {
		return false
	}

	if strings.ContainsAny(s, "/\\") {
		return false
	}

	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}

	return true
}

// checkLocalDest refuses a `SAVE ARTIFACT ... AS LOCAL` destination that is not
// inside the project.
//
// The destination is written to the machine running the build, and it comes
// from an Earthfile - which, since a remote reference makes this build
// interpret an Earthfile fetched from elsewhere, may be text an attacker wrote.
// An absolute path or one climbing out of the project is that Earthfile
// choosing where to write on this machine: a crontab, an authorized_keys, a
// shell profile. It is a place inside the project or it is refused.
func checkLocalDest(dest, where string) error {
	if dest == "" {
		return fmt.Errorf("SAVE ARTIFACT at %s: AS LOCAL needs a destination", where)
	}

	if filepath.IsAbs(dest) || strings.HasPrefix(dest, "~") {
		return fmt.Errorf(
			"SAVE ARTIFACT at %s: %q is not inside the project"+
				"\n  AS LOCAL writes relative to the Earthfile's own directory",
			where, dest)
	}

	if clean := filepath.Clean(dest); clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf(
			"SAVE ARTIFACT at %s: %q leaves the project directory"+
				"\n  AS LOCAL writes relative to the Earthfile's own directory",
			where, dest)
	}

	return nil
}

// pinnedRev reports whether a revision names one immutable commit.
//
// **A revision is not a pin.** `:main` and `:v1.2.3` are revisions and both are
// whatever the person with push access last made them - a tag can be moved, and
// on most forges by anyone who can push. Only an object name fixes what will be
// fetched, and only then can a reader check the commands before naming them.
//
// Full length, not a prefix: git resolves an abbreviated hash against the
// objects it happens to have, so a short one names different commits in
// different clones and is a pin only by luck. Both digest sizes are accepted
// because git is midway through changing them.
func pinnedRev(rev string) bool {
	if len(rev) != 40 && len(rev) != 64 {
		return false
	}

	for _, r := range rev {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
		if !isHex {
			return false
		}
	}

	return true
}
