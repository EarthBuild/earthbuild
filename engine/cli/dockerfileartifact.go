package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EarthBuild/earthbuild/engine/interp"
)

// artifacts builds a target and gives back where its output can be read.
//
// The capability `FROM DOCKERFILE +gen/` needs: the Dockerfile is parsed while
// planning, so a Dockerfile another target writes has to be built before the
// plan exists (E487). What happens here is an ordinary build of that target -
// planned, scheduled, and read - and its steps print like any others, because a
// target that ran and printed nothing is one the reader cannot account for.
//
// **Not given to a dry run.** That caller promises to resolve a plan and run
// nothing, and one that quietly built a target would be the single command here
// that lies about what it does (E488).
func (g *engine) artifacts(ctx context.Context, o Options, src string) interp.Artifacts {
	// A reference being built right now.
	//
	// **Not for the self-loop**: `gen: FROM DOCKERFILE +gen/` is caught by the
	// interpreter's own cycle detector, which says it better - "+gen -> +gen, a
	// target cannot depend on itself". This is for the loop that runs *between*
	// nested builds - `+a` planned from `+b`'s Dockerfile and `+b` from `+a`'s -
	// where each `interp.Build` is a fresh interpreter and neither one's cycle
	// detector can see the other's half. Without it that recurses until the
	// stack runs out, which names none of the Earthfile that caused it (E488).
	building := &sync.Map{}

	var fetch interp.Artifacts

	fetch = func(ref, where string) (string, error) {
		if _, going := building.LoadOrStore(ref, true); going {
			return "", fmt.Errorf(
				"%s is needed to plan itself"+
					"\n  a target cannot produce the Dockerfile its own base is"+
					" built from", ref)
		}

		defer building.Delete(ref)

		target, name := targetAndArtifact(ref)

		// **A reference may name a target in another Earthfile.** `+gen` is one
		// of the Earthfile being planned, and `../../..+cache-helper` is not -
		// planning the second against the first's text looks the name up in the
		// wrong file and reports "no such target" for a target that exists. The
		// split is the one the command line does at the front door, so a
		// reference means here what it means there.
		dir, want := splitTargetRef(o.Dir, target)

		text, err := sourceIn(dir, o.Dir, src)
		if err != nil {
			return "", err
		}

		no := nested(o, dir)

		sub, err := interp.Build(text, want,
			interp.WithContext(no.Dir),
			interp.WithContextCache(g.contexts),
			interp.WithArgs(no.Args),
			interp.WithSecrets(no.Secrets),
			interp.WithPlatform(no.platformOrDefault()),
			interp.WithCommands(g.commands(ctx)),
			interp.WithRemotes(g.remotes(ctx)),
			interp.WithGitClone(g.gitClone(ctx)),
			interp.WithVersionFlags(no.VersionFlags),
			// Passed down, so a Dockerfile-producing target may itself be
			// planned from a produced Dockerfile. The map above is what makes
			// that safe, and it is the only thing that can: each nested
			// `interp.Build` is a fresh interpreter, so the cycle detector
			// inside one cannot see a loop that runs *between* them (E488).
			interp.WithArtifacts(fetch))
		if err != nil {
			return "", fmt.Errorf("planning %s (%s): %w", target, where, err)
		}

		e, s, err := runPlan(ctx, no, sub, g, nil)
		if err != nil {
			return "", err
		}

		into, err := os.MkdirTemp("", "earthbuild-dockerfile-")
		if err != nil {
			return "", fmt.Errorf("nowhere to put what %s produced: %w", target, err)
		}

		for _, a := range sub.Artifacts {
			// The one the reference named, or all of them where it named the
			// whole output. `+gen/` is the context *and* the Dockerfile, and
			// which file that is depends on what the recipe saved.
			// **Matched by suffix as well as by name.** A reference is
			// relative to the producing target's working directory -
			// `+cache-helper/build/h.wasm` is `build/h.wasm` under whatever
			// WORKDIR that target set - and what is recorded here is the
			// artifact's own path inside the step, which is absolute.
			if name != "" && filepath.Base(a.Path) != name && a.Name != name &&
				!strings.HasSuffix(a.Path, "/"+name) {
				continue
			}

			stack := s.StackFor(a.From)
			if len(stack) == 0 {
				return "", fmt.Errorf("%s: the step producing %s did not run",
					target, a.Path)
			}

			// `ExportInternal`, because *this engine* chose the destination.
			//
			// `Export` refuses one outside the project, and the reason is about
			// `AS LOCAL`: it is the one command in the language that names a
			// path on the machine running the build, and an Earthfile is
			// routinely somebody else's code. A temporary directory the engine
			// made is not that, and the first real run of this path was refused
			// for writing outside a project it was never asked to write into
			// (E490).
			// Staged under the name the reference asked for, so the reader
			// finds it where it asked. Only where one was named: a whole-output
			// reference is a context, and its artifacts keep their own names.
			dest := dockerfileDest(into, a.Path)
			if name != "" {
				dest = filepath.Join(into, filepath.FromSlash(name))
			}

			err := e.ExportInternal(ctx, stack, a.Path, dest, a.IfExists)
			if err != nil {
				return "", err
			}
		}

		return into, nil
	}

	return fetch
}

// nested is the invocation a target built so that a plan can be made runs
// under.
//
// **Its AS LOCAL exports are not the caller's.** `--helper +h/build/h.wasm` and
// `FROM DOCKERFILE +gen/` name an artifact the way `COPY` does, and a `COPY`
// does not run the named target's local exports. Built as an ordinary
// invocation it did - and an `AS LOCAL` destination is relative to the
// directory the *caller* started in, not to the Earthfile the target came from.
// So planning `examples/cache-helpers/go-build+compile` ran the repository
// root's `SAVE ARTIFACT go.mod AS LOCAL go.mod` and wrote the engine's own
// go.mod over the example's.
//
// `NoOutput` says exactly this and already existed: the steps still run and the
// cache still fills, and the only thing withheld is the write to somebody's
// working tree. What the caller actually asked for is exported separately, into
// a directory this engine made.
//
// By value, so the caller's options are untouched.
func nested(o Options, dir string) Options {
	o.Dir = dir
	o.NoOutput = true

	return o
}

// sourceIn is the Earthfile a nested build is planned from.
//
// The entry Earthfile has been read already, so it is handed in rather than
// read a second time; any other is read from beside the target it holds.
func sourceIn(dir, entry, src string) (string, error) {
	if sameDir(dir, entry) {
		return src, nil
	}

	at := filepath.Join(dir, "Earthfile")

	b, err := os.ReadFile(at) //nolint:gosec // a directory named by the build being planned
	if err != nil {
		return "", fmt.Errorf("read %s: %w", at, err)
	}

	return string(b), nil
}

// sameDir says whether two spellings name one directory.
//
// Through EvalSymlinks, because one side comes from the invocation and the
// other from a reference resolved against it, and on macOS `/var` and
// `/private/var` are the same directory spelled two ways.
func sameDir(a, b string) bool {
	return resolveDir(a) == resolveDir(b)
}

func resolveDir(at string) string {
	abs, err := filepath.Abs(at)
	if err != nil {
		return at
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}

	return resolved
}

// targetAndArtifact splits `+gen/other.Dockerfile` into its two halves.
//
// A reference ending in `/` names the whole output and no particular file, which
// is the form `FROM DOCKERFILE +gen/` uses for a context.
func targetAndArtifact(ref string) (target, name string) {
	// **The first separator after the `+`, which is where `COPY` cuts.** At the
	// last one, `+cache-helper/build/h.wasm` asked for a target called
	// `+cache-helper/build` - right for a one-segment artifact and wrong for
	// every deeper one, silently, as a cache that does not share.
	plus := strings.LastIndex(ref, "+")
	if plus < 0 {
		return ref, ""
	}

	i := strings.Index(ref[plus:], "/")
	if i < 0 {
		return ref, ""
	}

	return ref[:plus+i], ref[plus+i+1:]
}

// dockerfileDest is where one of a target's artifacts is put so the Dockerfile
// can be read from beside it.
//
// **A pattern is already a directory.** `SAVE ARTIFACT ./*` is recorded with the
// path `/test/*`, and taking `filepath.Base` of that named the destination `*` -
// so the export landed in a directory of that name and the reader looking for
// `<tmp>/Dockerfile` found nothing (tests/gen-dockerfile.earth,
// tests/from-dockerfile-arg.earth).
//
// The export stages a pattern into a directory of its own holding each match
// under its own name, so it copies out *as* this directory rather than into a
// subdirectory of it. A plain path keeps its own name, which is what the reader
// asks for.
func dockerfileDest(into, path string) string {
	if strings.ContainsAny(filepath.Base(path), "*?[") {
		return into
	}

	return filepath.Join(into, filepath.Base(path))
}
