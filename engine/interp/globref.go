package interp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// expandRef turns a reference whose path is a pattern into one reference per
// directory it matches.
//
// **`BUILD ./wildcard/*+test` names every matching directory's target.** The
// corpus writes it five ways - `*`, `**`, a character class, a bare `./*`, and a
// path climbing out with `..` - and this engine read all of them literally,
// looking for a directory named `*` and reporting that it was not there.
//
// A reference with no metacharacter is returned as written and the filesystem is
// never consulted: a plain reference to a directory that does not exist must
// still reach the resolver, which explains itself far better than a glob that
// matched nothing.
//
// **Only directories holding an Earthfile match.** The pattern is over places a
// target could live, not over names, and offering a directory with no Earthfile
// would turn a tidy "no such target" into a confusing one.
//
// **Sorted, and that is load-bearing.** A reference expanding to several targets
// contributes them in the order given; taking the filesystem's order would key
// one Earthfile differently on two machines, which is I1 lost to a directory
// listing.
func expandRef(dir, ref string) ([]string, error) {
	at := strings.LastIndex(ref, "+")
	if at <= 0 {
		return []string{ref}, nil
	}

	pattern, name := ref[:at], ref[at+1:]
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{ref}, nil
	}

	matches, err := globDirs(dir, pattern)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m+"+"+name)
	}

	sort.Strings(out)

	return out, nil
}

// globDirs is every directory under dir matching the pattern and holding an
// Earthfile, named as the pattern named them - relative, with the `./` the
// author wrote.
func globDirs(dir, pattern string) ([]string, error) {
	rooted := pattern
	if !filepath.IsAbs(pattern) {
		rooted = filepath.Join(dir, pattern)
	}

	var found []string

	paths, err := expandDoubleStar(rooted)
	if err != nil {
		return nil, err
	}

	for _, p := range paths {
		fi, statErr := os.Stat(p)
		if statErr != nil || !fi.IsDir() {
			continue
		}

		_, statErr = os.Stat(filepath.Join(p, "Earthfile"))
		if statErr != nil {
			continue
		}

		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			continue
		}

		// The `./` the author wrote is kept, because the resolver reads a
		// leading `./` as "beside this Earthfile" and a bare name as an import.
		if strings.HasPrefix(pattern, "./") {
			rel = "./" + rel
		}

		found = append(found, rel)
	}

	return found, nil
}

// expandDoubleStar is filepath.Glob with `**` meaning any number of directories.
//
// `filepath.Glob` has no `**`: it reads one as an ordinary `*` and so matches a
// single level. The corpus means any depth, so each `**` is replaced by every
// depth it could stand for and the results globbed as usual.
func expandDoubleStar(pattern string) ([]string, error) {
	before, after, found := strings.Cut(pattern, "**")
	if !found {
		return filepath.Glob(pattern)
	}

	base := filepath.Dir(before)

	var out []string

	// Every directory at or below the fixed part, each standing in for the
	// `**`, and the rest of the pattern globbed under it.
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || !d.IsDir() {
			//nolint:nilerr // a directory this cannot read matches nothing
			return nil
		}

		here, globErr := expandDoubleStar(p + strings.TrimPrefix(after, "/"))
		if globErr != nil {
			return globErr
		}

		out = append(out, here...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// errNoSuchTarget marks an Earthfile that does not define the target asked for.
//
// **A pattern skips what it cannot build; a name does not.** `BUILD
// ./wildcard/*+test` is written against a tree where one directory holds an
// Earthfile with a different target - the corpus keeps `no-target` there for
// exactly this - and a glob that failed on it could never match anything useful.
// `BUILD ./no-target+test` names one directory and must still say what is wrong.
//
// Distinguished by a sentinel rather than by the text, because the text is a
// diagnostic and diagnostics are rewritten.
var errNoSuchTarget = errors.New("no such target")
