package interp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EarthBuild/earthbuild/earthfile2llb/cmdopts"
	"github.com/EarthBuild/earthbuild/util/flagutil"

	"github.com/EarthBuild/earthbuild/engine/ir"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
)

// unit is one Earthfile: its tree, the directory it lives in, and what has been
// resolved from it.
//
// Per-file rather than per-build because everything in an Earthfile is relative
// to *its own* directory. A COPY in `lib/Earthfile` names a file beside that
// file, and resolving it against the calling Earthfile would silently copy
// something else - or report a file missing that is sitting exactly where its
// own Earthfile says it is.
type unit struct {
	tree earthfile.Tree
	// features are what this file's VERSION line opted into. Per file, because
	// a VERSION line is a declaration a file makes about itself.
	features features
	// ended is the state each resolved target finished in, which is what
	// `FROM +target` continues from: a base target that sets WORKDIR and ENV
	// exists so that what builds on it inherits them. Keyed the same way as
	// resolved, because a target built with different arguments ends in a
	// different state.
	ended map[string]*state
	// dir is this Earthfile's directory: its build context, and the root that
	// its relative references are resolved against.
	dir string
	// confinedTo bounds where this Earthfile's references may reach, empty for
	// no bound.
	//
	// It is set for a unit that came from a fetched checkout and is inherited by
	// everything that unit loads. The rule is about provenance, not about the
	// path: `FROM ../../..+t` in an Earthfile on this machine is ordinary and
	// the corpus is full of it, but in one fetched from elsewhere it climbs out
	// of the cache and lets a remote repository name any Earthfile on this
	// machine and have it built.
	confinedTo string
	// reachedUnpinned says some link in the chain that led here named a branch
	// or a tag rather than a commit, so what this file contains can change
	// after somebody decided to trust it. False for the Earthfile in front of
	// you, which is nobody's to change but yours.
	reachedUnpinned bool
	// fetchedFrom names the repository this Earthfile came from, empty for one
	// on this machine.
	//
	// Beside confinedTo rather than derived from it, because the two answer
	// different questions: confinedTo is *where* the references may reach, and
	// this is *whose* they are. A remote target that runs on the host runs a
	// command chosen by whoever can push to that repository, as the person who
	// typed the build - so the refusal has to name them (E439).
	fetchedFrom string

	// imports are the names this Earthfile has given to others: IMPORT.
	//
	// Per-file, because an import is a declaration *in* a file about how that
	// file's own references read. A name imported in one Earthfile means nothing
	// in another, and sharing them would let a reference resolve differently
	// depending on which file happened to be parsed first.
	imports map[string]string
	// grants are the imported names that carried `--allow-privileged`.
	//
	// **The flag is on the import, so every reference through the name
	// inherits it** - which is the point of naming a repository once.
	// `allow-privileged-import.earth` grants on the IMPORT line and then writes
	// `COPY privileged+privileged/proc-status .` with no flag of its own.
	grants map[string]bool

	resolved  map[string]*ir.Node
	baseDone  bool
	baseNode  *ir.Node
	baseState *state
}

// newUnit builds a unit with its maps.
//
// A constructor rather than two struct literals: the build's first unit was
// built inline and the rest through load, so a field added to one was missing
// from the other and the first IMPORT in any file panicked on a nil map.
func newUnit(tree earthfile.Tree, dir string, overrides []string) (*unit, error) {
	// Read once, here, because every unit has a VERSION line and every gate
	// asks the same question of it.
	f, err := readFeatures(tree.Version, overrides)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "Earthfile"), err)
	}

	u := &unit{
		tree:     tree,
		dir:      dir,
		features: f,
		imports:  map[string]string{},
		grants:   map[string]bool{},
		resolved: map[string]*ir.Node{},
	}

	u.collectImports()

	return u, nil
}

// collectImports reads the file's IMPORT lines the moment it is loaded.
//
// **An IMPORT is a declaration about the file, not a step in it.** The map used
// to be filled only when the command was *interpreted*, which happens while
// walking a unit's base recipe - and a unit entered at one of its functions is
// never walked that way. So a function calling through an alias its own file
// declared was told the alias "was never imported", which is
// `earthly-command-example/import/Earthfile` reached from
// `tests/import.earth+test-command-import`.
//
// References resolve against the defining file, so the defining file has to know
// its own imports before anything asks. Interpreting the line again is harmless
// and still happens: it writes the same two entries.
//
// A malformed IMPORT is left to the interpreter, which reports it with a source
// location. Refusing to load the file here would name the whole unit for a fault
// on one line, and a file whose base recipe is never walked would be refused for
// a line nothing was going to read.
func (u *unit) collectImports() {
	for _, c := range u.tree.BaseRecipe {
		if c.Command == nil || c.Command.Name != earthfile.CmdImport {
			continue
		}

		name, path, grant, err := importParts(c.Command.Args)
		if err != nil {
			continue
		}

		u.imports[name] = path

		if grant {
			u.grants[name] = true
		}
	}
}

// realDir is the one way a directory becomes comparable to another.
//
// Absolute *and* symlink-resolved, because the two must agree: `load` resolves
// symlinks, so a confinement root that did not would compare `/var/...` against
// `/private/var/...` and refuse every reference on a Mac.
func realDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", dir, err)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	}

	return abs, nil
}

// confine reports the error for a reference that leaves its checkout.
func (u *unit) confine(dir string) error {
	if u.confinedTo == "" {
		return nil
	}

	abs, err := realDir(dir)
	if err != nil {
		return err
	}

	if abs != u.confinedTo && !strings.HasPrefix(abs, u.confinedTo+string(filepath.Separator)) {
		return fmt.Errorf(
			"this Earthfile came from a fetched checkout and refers outside it"+
				"\n  it may only refer to Earthfiles within %s", u.confinedTo)
	}

	return nil
}

// load reads and parses an Earthfile, once per directory.
func (p *Plan) load(dir string) (*unit, error) {
	abs, err := realDir(dir)
	if err != nil {
		return nil, err
	}

	if u, ok := p.units[abs]; ok {
		return u, nil
	}

	path := filepath.Join(abs, "Earthfile")

	src, err := os.ReadFile(path) //nolint:gosec // a path the Earthfile named
	if err != nil {
		return nil, fmt.Errorf("no Earthfile for this reference\n  looked for %s", path)
	}

	tree, err := earthfile.Parse(path, string(src), earthfile.WithSourceMap())
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	u, err := newUnit(tree, abs, p.opt.versionFlags)
	if err != nil {
		return nil, err
	}

	p.units[abs] = u

	return u, nil
}

// reference is a target or function named from somewhere.
type reference struct {
	// dir is where the Earthfile is, empty for the current one.
	dir string
	// name is the target or function.
	name string
	// remote is set when the reference names another repository, in which case
	// dir is empty until it has been fetched.
	remote *remote
}

// importPath records `IMPORT <path> [AS <name>]`.
//
// Without AS the alias is the last element of the path, which is how the
// documentation writes it and how most of this repository does.
// importParts also reports whether the IMPORT granted privilege.
//
// Separate from importPath so the existing callers keep their two results; the
// grant matters only where an import is recorded.
func importParts(args []string) (name, path string, grant bool, err error) {
	if len(args) == 0 {
		return "", "", false, errors.New("IMPORT needs a path")
	}

	// The flags first, so the path is the path. `IMPORT --allow-privileged
	// github.com/org/repo:main` took the flag as the reference and registered a
	// name from it, and the file's own `COPY repo+t/x .` then failed with "repo
	// was never imported" - pointing at the use, one line below the declaration
	// that was right there (E440).
	//
	// The same shape as `ARG --global IMAGE=...` declaring an argument called
	// `--global`, and read with the same option layer for the same reason: a
	// hand-rolled skip is a second parser, and the two disagree about the first
	// flag either of them has not heard of.
	var opts cmdopts.Import

	rest, perr := flagutil.ParseArgsCleaned("IMPORT", &opts, args)
	if perr == nil && len(rest) > 0 {
		args = rest
	}

	path = args[0]
	name = strings.TrimSuffix(filepath.Base(path), "/")

	// The revision is not part of the name: `IMPORT github.com/org/repo:main`
	// is called `repo`, not `repo:main`. This repository's own example Earthfile
	// says so in a comment beside the line, which is where the expected
	// behaviour was found after the alias failed to resolve.
	if i := strings.Index(name, ":"); i >= 0 {
		name = name[:i]
	}

	for i := 1; i < len(args); i++ {
		if !strings.EqualFold(args[i], "AS") {
			continue
		}

		if i+1 >= len(args) {
			return "", "", false, fmt.Errorf("IMPORT %s: AS needs a name", path)
		}

		name = args[i+1]

		break
	}

	if name == "" || name == "." || name == ".." {
		return "", "", false, fmt.Errorf(
			"IMPORT %s: cannot tell what to call this\n  give it a name with AS", path)
	}

	return name, path, opts.AllowPrivileged, nil
}

// parseRef splits `+name`, `./path+name`, `../..+name` and refuses the rest.
//
// Remote references - `github.com/org/repo+target` - need a checkout and a
// network, and are refused rather than guessed at: silently building something
// other than what was named is the failure this engine is arranged against.
func parseRef(s, where string, imports map[string]string) (reference, error) {
	// The *last* plus, for the reason `separator` gives: a target name cannot
	// contain one, so in `./dir-with-+-in-it+test` only the last can divide the
	// path from the name (E444). Cutting at the first looked for an Earthfile in
	// `./dir-with-`.
	i := strings.LastIndex(s, "+")
	if i < 0 {
		return reference{}, fmt.Errorf("%q is not a target reference (%s)", s, where)
	}

	before, after := s[:i], s[i+1:]

	path, name := before, after
	if name == "" {
		return reference{}, fmt.Errorf("%q names no target (%s)", s, where)
	}

	// An imported name resolves to whatever the IMPORT said, and is looked up
	// before anything else: `tests+build` is a name this file gave, not a
	// directory called "tests".
	if path != "" && !strings.HasPrefix(path, ".") && !strings.HasPrefix(path, "/") {
		if to, ok := imports[path]; ok {
			// The alias may stand for a repository rather than a directory.
			if !strings.HasPrefix(to, ".") && !strings.HasPrefix(to, "/") {
				r, err := parseRemote(to, where)
				if err != nil {
					return reference{}, err
				}

				return reference{remote: &r, name: name}, nil
			}

			return reference{dir: to, name: name}, nil
		}

		// A bare name that was never imported. Reading it as a relative path
		// would report "no Earthfile in ./tests" - true, and unhelpful, when the
		// real answer is that a line is missing.
		if !strings.Contains(path, ".") && !strings.Contains(path, "/") {
			return reference{}, fmt.Errorf(
				"%q was never imported (%s)"+
					"\n  add `IMPORT <path> AS %s`, or write the path directly as ./%s+%s",
				path, where, path, path, name)
		}

		r, err := parseRemote(path, where)
		if err != nil {
			return reference{}, err
		}

		return reference{remote: &r, name: name}, nil
	}

	return reference{dir: path, name: name}, nil
}

// resolve turns a reference into the unit it names, relative to the one it was
// written in.
func (p *Plan) resolve(from *unit, ref reference) (*unit, error) {
	if ref.remote != nil {
		dir, err := p.dirFor(*ref.remote, from.dir)
		if err != nil {
			return nil, err
		}

		root, err := realDir(dir)
		if err != nil {
			return nil, err
		}

		u, err := p.load(filepath.Join(root, ref.remote.subdir))
		if err != nil {
			return nil, err
		}

		// The checkout root, not the subdirectory: a reference into a sibling
		// directory of the same repository is the repository's own business.
		u.confinedTo = root
		u.fetchedFrom = ref.remote.repo

		// **The chain, not the link.** A pinned repository that imports an
		// unpinned one has moved the choice one hop away rather than removed
		// it, so a pin only counts when everything in front of it was pinned
		// too.
		u.reachedUnpinned = from.reachedUnpinned || !pinnedRev(ref.remote.rev)

		return u, nil
	}

	if ref.dir == "" {
		return from, nil
	}

	dir := filepath.Join(from.dir, ref.dir)
	err := from.confine(dir)
	if err != nil {
		return nil, err
	}

	u, err := p.load(dir)
	if err != nil {
		return nil, err
	}

	// Provenance is inherited: an Earthfile reached from a fetched one is just
	// as fetched, however local its own references look.
	if u.confinedTo == "" {
		u.confinedTo = from.confinedTo
		u.fetchedFrom = from.fetchedFrom
		u.reachedUnpinned = from.reachedUnpinned
	}

	return u, nil
}
