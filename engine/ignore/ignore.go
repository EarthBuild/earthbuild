// Package ignore is what a build context leaves out.
//
// **A build context that includes untracked files is not reproducible.** The
// engine digests what a COPY names to key the step, so anything lying in the
// directory is part of the key: a developer who has run `npm install` and a
// fresh clone of the same commit compute different keys and share no cache. It
// is the same class of defect as a layer named by when it was placed (E545),
// arriving one level up - the layers were made reproducible and the thing they
// are made from was not (E562).
//
// The file names and the matching are the reference engine's, from
// `buildcontext/excludes.go`: this engine is meant to be a drop-in, and a
// context that means one thing under one engine and another under the other is
// worse than no ignore file at all.
package ignore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// The files a context may use to say what it leaves out, in the order they are
// looked for. `.earthignore` and `.earthlyignore` are the same thing under two
// names and having both is refused rather than guessed at.
const (
	earthIgnore   = ".earthignore"
	earthlyIgnore = ".earthlyignore"
	dockerIgnore  = ".dockerignore"
)

// Implicit is what a context leaves out whether a file says so or not, and it
// is empty.
//
// **`--no-implicit-ignore` is `enabled_in_version:"0.6"`**, and this engine
// accepts no Earthfile older than that - so for every file it can build, the
// reference puts `.tmp-earth-out/`, `build.earth`, `Earthfile`, `.earthignore`
// and `.earthlyignore` in the context like anything else.
//
// Considered and rejected: keeping them out anyway, on the argument that the
// build's own description is not one of its inputs, so including it in a COPY's
// digest makes every COPY miss whenever any line of the Earthfile moves. That is
// true and it is not this engine's decision to make - `tests/no-implicit-ignore`
// does `COPY . .` and then `RUN ls Earthfile`, and a project that wants the
// Earthfile out of its context writes one line in `.earthlyignore`.
//
// Kept as a name rather than deleted, because the list is the thing a reader
// goes looking for, and an empty one with this note answers them.
var Implicit []string

// Matcher decides whether a path is left out of the context.
//
// The zero value excludes nothing, which is what a context with no ignore file
// wants and what this engine did before any of this.
type Matcher struct {
	m *patternmatcher.PatternMatcher
}

// Read collects a context root's exclusions.
//
// A missing ignore file is not an error - most contexts have none - but a
// malformed one is: a pattern nobody can parse is a pattern that was meant to
// exclude something, and carrying on would silently include it.
func Read(root string) (Matcher, error) {
	patterns := append([]string(nil), Implicit...)

	named, err := ignoreFileIn(root)
	if err != nil {
		return Matcher{}, err
	}

	if named != "" {
		f, opened := os.Open(named) //nolint:gosec // a path derived from the context root
		if opened != nil {
			return Matcher{}, fmt.Errorf("read %s: %w", filepath.Base(named), opened)
		}

		defer func() { _ = f.Close() }()

		more, parsed := ignorefile.ReadAll(f)
		if parsed != nil {
			return Matcher{}, fmt.Errorf("parse %s: %w", filepath.Base(named), parsed)
		}

		patterns = append(patterns, more...)
	}

	m, err := patternmatcher.New(patterns)
	if err != nil {
		return Matcher{}, fmt.Errorf("the context's exclusions: %w", err)
	}

	return Matcher{m: m}, nil
}

// Excludes reports whether a path relative to the context root is left out.
//
// Slash-separated, because that is what a pattern is written in and what the
// matcher expects; a caller walking a filesystem converts.
func (m Matcher) Excludes(rel string) bool {
	if m.m == nil || rel == "" || rel == "." {
		return false
	}

	out, err := m.m.MatchesOrParentMatches(rel)

	return err == nil && out
}

// Empty reports whether this matcher would exclude nothing a caller cares
// about, so a walk can skip asking.
func (m Matcher) Empty() bool { return m.m == nil }

// ignoreFileIn names the ignore file a context uses, or empty.
func ignoreFileIn(root string) (string, error) {
	earth := filepath.Join(root, earthIgnore)
	earthly := filepath.Join(root, earthlyIgnore)

	_, earthErr := os.Stat(earth)
	_, earthlyErr := os.Stat(earthly)

	if earthErr == nil && earthlyErr == nil {
		return "", errors.New("both .earthignore and .earthlyignore exist - please remove one")
	}

	if earthErr == nil {
		return earth, nil
	}

	if earthlyErr == nil {
		return earthly, nil
	}

	// Docker's, last, so a project that has one and no Earthfile-specific one
	// gets what it plainly meant.
	docker := filepath.Join(root, dockerIgnore)

	_, dockerErr := os.Stat(docker)
	if dockerErr == nil {
		return docker, nil
	}

	return "", nil
}

// Excluder decides whether a path under some walk root is left out.
//
// The walk's root is not always the context's root - a `COPY engine/ ...`
// walks `engine/` while the ignore file speaks about `engine/store/testdata/...`
// - so an excluder carries the prefix that turns one into the other.
type Excluder struct {
	m    Matcher
	from string
}

// matchers is one parsed ignore file per context root, for this process.
//
// **Per process, which is per build.** The engine's front end is a one-shot
// command: it reads the ignore file, plans, builds and exits, so a file edited
// between two builds is read again by the second one. A long-lived caller that
// wanted to see an edit mid-process would need a different rule, and there is no
// such caller.
var matchers sync.Map // root -> Matcher

// For reads a context's ignore file once and reuses it, for a walk under `under`.
//
// **One definition, because two would drift.** The interpreter uses this to
// decide what a context's digest covers and the executor uses it to decide what
// gets staged, and those two answers must be the same answer: a context whose
// contents do not match the identity computed for it is a layer nothing
// downstream can reason about (E623).
//
// Three scopes were possible for the caching and the first two were wrong. Once
// per *entry* is what the first version did - `Excludes` is called for every path
// in a walk, so parsing a file there costs more than the files it excludes, and
// it would have surfaced as "the optimisation made it slower" and been believed.
// Once per *digest* is 42 reads of one small file for this repository. Once per
// root is the one that matches what the answer depends on.
func For(root, under string) Excluder {
	m, cached := matchers.Load(root)
	if !cached {
		// A malformed ignore file excludes nothing rather than everything. The
		// build then digests more than it should, which is slow and correct; the
		// opposite silently drops files a COPY named.
		read, err := Read(root)
		if err != nil {
			read = Matcher{}
		}

		m, _ = matchers.LoadOrStore(root, read)
	}

	matcher, _ := m.(Matcher)

	from, err := filepath.Rel(root, under)
	if err != nil || from == "." {
		from = ""
	}

	return Excluder{m: matcher, from: from}
}

// Excludes reports whether a path under the walk's root is left out.
func (e Excluder) Excludes(rel string) bool {
	if e.from == "" {
		return e.m.Excludes(rel)
	}

	return e.m.Excludes(filepath.ToSlash(filepath.Join(e.from, rel)))
}
