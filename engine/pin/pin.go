// Package pin writes an Earthfile's image references down as digests.
//
// A reference that names a tag costs a registry round trip on every invocation -
// 0.60s of planning against 0.03s for one that names a digest - because the
// digest is what keys the cache and even a build with nothing to do must have it
// (E534). A reference that names both costs nothing and says more: the tag is
// what a reader recognises, the digest is what makes the build reproducible.
//
// The rewrite is textual on purpose. Reprinting from the parsed form would
// return a file laid out the way this engine likes it rather than the way its
// author wrote it, and a tool that reformats a file it was asked to annotate is
// a tool nobody runs twice.
package pin

import (
	"bytes"
	"fmt"
	"strings"
)

// Change is one reference this tried to pin.
//
// Failures are here too, with Err set and To empty: a caller that reports only
// what it pinned is silent about what it could not, which reads as nothing to do.
type Change struct {
	From string
	To   string
	Err  error
	// Line is 1-based, as an editor counts. Last, so the pointer-bearing fields
	// above sit together (govet fieldalignment).
	Line int
}

// Rewrite returns the file with every image reference pinned, and what it did.
//
// resolve is given a reference and returns it with a digest. A reference it
// cannot answer for is left exactly as written - the same trade the resolver
// makes during a build, where an unreachable registry means a coarser key rather
// than a failed build.
func Rewrite(src []byte, resolve func(string) (string, error)) ([]byte, []Change, error) {
	var (
		out     bytes.Buffer
		changes []Change
		// One reference named twice is one registry round trip.
		seen = map[string]string{}
	)

	// SplitAfter keeps the line endings, so a file with no trailing newline
	// still has none afterwards and one with CRLF keeps its CRLF.
	for i, line := range strings.SplitAfter(string(src), "\n") {
		if i > 0 && line == "" {
			// The empty remainder after a trailing newline.
			continue
		}

		rewritten, c := pinLine(line, i+1, seen, resolve)
		if c != nil {
			changes = append(changes, *c)
		}

		out.WriteString(rewritten)
	}

	return out.Bytes(), changes, nil
}

// pinLine rewrites one line, or returns it unchanged.
func pinLine(
	line string, at int, seen map[string]string, resolve func(string) (string, error),
) (string, *Change) {
	ref, start, end := reference(line)
	if ref == "" {
		return line, nil
	}

	to, ok := seen[ref]
	if !ok {
		var err error

		to, err = resolve(ref)
		if err != nil {
			return line, &Change{Line: at, From: ref, Err: err}
		}

		seen[ref] = to
	}

	return line[:start] + to + line[end:], &Change{Line: at, From: ref, To: to}
}

// reference finds the image a FROM names, and where it sits in the line.
//
// Empty when the line names no image this can pin, which covers rather a lot:
// a target reference has no digest to name, `scratch` is not a registry's to
// answer for, a reference built from an argument is not knowable until the build
// runs, `FROM DOCKERFILE` names a build context rather than an image, and one
// that already carries a digest is already what this produces.
func reference(line string) (ref string, start, end int) {
	rest := strings.TrimLeft(line, " \t")

	indent := len(line) - len(rest)
	if !strings.HasPrefix(rest, "FROM") {
		return "", 0, 0
	}

	rest = rest[len("FROM"):]
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return "", 0, 0
	}

	at := indent + len("FROM")

	// Flags come before the reference and stay where they are.
	for {
		gap := len(rest) - len(strings.TrimLeft(rest, " \t"))
		rest = rest[gap:]
		at += gap

		word := rest
		if i := strings.IndexAny(rest, " \t\r\n"); i >= 0 {
			word = rest[:i]
		}

		if word == "" {
			return "", 0, 0
		}

		if !strings.HasPrefix(word, "--") {
			if !pinnable(word) {
				return "", 0, 0
			}

			return word, at, at + len(word)
		}

		rest = rest[len(word):]
		at += len(word)
	}
}

// pinnable reports whether a word is an image reference with a digest to gain.
func pinnable(word string) bool {
	switch {
	case word == "scratch", word == "DOCKERFILE":
		return false
	// A target, here or in another directory. `+` cannot appear in a reference.
	case strings.Contains(word, "+"):
		return false
	// Built from an argument, and not knowable until the build runs.
	case strings.Contains(word, "$"):
		return false
	// Already the thing this produces.
	case strings.Contains(word, "@"):
		return false
	}

	return true
}

// WithDigest is the reference as written, plus the digest a resolution found.
//
// A resolver answers in its own canonical form: the repository and the digest,
// with the tag dropped. That is the right record of *what ran* and the wrong
// thing to write into a file somebody reads - the tag says which version they
// are on, and it is what renovate's docker datasource matches to keep both
// halves moving. So the digest is taken and the reference is otherwise left
// exactly as its author wrote it.
func WithDigest(ref, resolved string) (string, error) {
	_, dg, ok := strings.Cut(resolved, "@")
	if !ok || dg == "" {
		return "", fmt.Errorf("resolving %s produced %q, which names no digest", ref, resolved)
	}

	return ref + "@" + dg, nil
}
