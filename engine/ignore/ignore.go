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

// Implicit is excluded from every context whether a file says so or not.
//
// The build's own description is not one of its inputs: an Earthfile that
// changes changes the build, and including it in a COPY's digest would make
// every COPY miss whenever any line of the Earthfile moved.
var Implicit = []string{
	".tmp-earth-out/",
	"build.earth",
	"Earthfile",
	earthIgnore,
	earthlyIgnore,
}

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
