package interp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/internal/earthfile"
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

	matches, err := globDirs(dir, pattern, name)
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
func globDirs(dir, pattern, target string) ([]string, error) {
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

		// **And it must define the target.** The corpus keeps
		// `tests/wildcard/no-target` beside the others precisely to check this:
		// a pattern that failed on the one directory whose Earthfile says
		// something else could never match a useful set. A reference naming one
		// directory is a different matter and still says what is wrong, because
		// it never reaches here.
		if !defines(filepath.Join(p, "Earthfile"), target) {
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

// expandArtifactRef is expandRef for a `COPY` source, where an artifact path
// follows the target name.
//
// `COPY ./wildcard/*+test/out.txt ./` takes that artifact from every matching
// target. The pattern is over *which target*, and the artifact path after the
// target name is carried through untouched: a pattern there would name files
// inside another target's output, which nothing at this point can list.
//
// Thirteen of the corpus's invocations are this form, against five for `BUILD`.
func expandArtifactRef(dir, src string) ([]string, error) {
	at := strings.LastIndex(src, "+")
	if at <= 0 {
		return []string{src}, nil
	}

	path := src[:at]
	if !strings.ContainsAny(path, "*?[") {
		return []string{src}, nil
	}

	// The target's name ends at the first separator after the `+`; everything
	// from there is the artifact.
	rest := src[at+1:]

	name, artifact := rest, ""
	if slash := strings.Index(rest, "/"); slash >= 0 {
		name, artifact = rest[:slash], rest[slash:]
	}

	refs, err := expandRef(dir, path+"+"+name)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r+artifact)
	}

	return out, nil
}

// defines reports whether an Earthfile has a target of this name.
//
// Parsed rather than scanned: a target header is not simply a line ending in a
// colon, and a pattern that quietly skipped a real target would be a build
// missing a piece with nothing said about it.
//
// An Earthfile that will not parse defines nothing here. That is not a
// judgement on the file - whoever names it directly still gets the parser's own
// account of what is wrong with it - only a statement that a pattern will not
// adopt it.
func defines(path, target string) bool {
	tree, err := earthfile.ParseFile(path)
	if err != nil {
		return false
	}

	for _, t := range tree.Targets {
		if t.Name == target {
			return true
		}
	}

	return false
}
