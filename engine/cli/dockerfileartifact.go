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

		sub, err := interp.Build(src, target,
			interp.WithContext(o.Dir),
			interp.WithArgs(o.Args),
			interp.WithSecrets(o.Secrets),
			interp.WithPlatform(o.platformOrDefault()),
			interp.WithCommands(g.commands(ctx)),
			interp.WithRemotes(g.remotes(ctx)),
			interp.WithGitClone(g.gitClone(ctx)),
			interp.WithVersionFlags(o.VersionFlags),
			// Passed down, so a Dockerfile-producing target may itself be
			// planned from a produced Dockerfile. The map above is what makes
			// that safe, and it is the only thing that can: each nested
			// `interp.Build` is a fresh interpreter, so the cycle detector
			// inside one cannot see a loop that runs *between* them (E488).
			interp.WithArtifacts(fetch))
		if err != nil {
			return "", fmt.Errorf("planning %s (%s): %w", target, where, err)
		}

		e, s, err := runPlan(ctx, o, sub, g, nil)
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
			if name != "" && filepath.Base(a.Path) != name && a.Name != name {
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
			dest := filepath.Join(into, filepath.Base(a.Path))
			err := e.ExportInternal(ctx, stack, a.Path, dest, a.IfExists)
			if err != nil {
				return "", err
			}
		}

		return into, nil
	}

	return fetch
}

// targetAndArtifact splits `+gen/other.Dockerfile` into its two halves.
//
// A reference ending in `/` names the whole output and no particular file, which
// is the form `FROM DOCKERFILE +gen/` uses for a context.
func targetAndArtifact(ref string) (target, name string) {
	i := strings.LastIndex(ref, "/")
	if i < 0 {
		return ref, ""
	}

	return ref[:i], ref[i+1:]
}
