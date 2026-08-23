package subcmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/engine/cli"
)

// nativeEngine is what --engine=native names.
const nativeEngine = "native"

// runNative builds through the native engine instead of buildkit.
//
// **The point is to run one Earthfile both ways on one machine.** The native
// engine has been developed against a single darwin arm64 laptop; every gap it
// has against the engine that ships is found by comparing them, and the
// comparison is only cheap if it is a flag rather than a second binary and a
// second invocation (E593).
//
// A refusal rather than a silent fallback where the two do not line up: a
// remote target or an artifact reference means the caller asked for something
// this path does not carry across, and quietly building something else is how a
// comparison stops comparing.
func (b *Build) runNative(ctx context.Context, target domain.Target, flagArgs []string) error {
	if target.IsRemote() {
		return fmt.Errorf(
			"--engine=%s cannot build %s: it is a remote target, and this engine"+
				" builds the Earthfile in front of it"+
				"\n  build it from a checkout, or use --engine=buildkit",
			nativeEngine, target.String())
	}

	var platform string

	if p := b.platformsStr; len(p) > 1 {
		return fmt.Errorf(
			"--engine=%s was given %d platforms and builds one at a time"+
				"\n  name a single --platform, or use --engine=buildkit",
			nativeEngine, len(p))
	} else if len(p) == 1 {
		platform = p[0]
	}

	dir := target.LocalPath
	if dir == "" {
		dir = "."
	}

	// `NAME=VALUE`, which is what both engines' `--build-arg` means. A bare name
	// is the shell's value in the reference and is refused here rather than
	// guessed at, because a build that silently took a different value is worse
	// than one that stops.
	args := map[string]string{}

	for _, a := range flagArgs {
		name, value, ok := strings.Cut(a, "=")
		if !ok {
			return fmt.Errorf(
				"--engine=%s: build argument %q has no value"+
					"\n  write it as %s=<value>", nativeEngine, a, a)
		}

		args[name] = value
	}

	return cli.Run(ctx, cli.Options{
		Dir:    dir,
		Target: "+" + target.Target,
		// One platform, because this engine builds for one at a time: the
		// reference takes a list and fans out, and taking the first of a list
		// silently would build something the caller did not ask for.
		Platform: platform,
		Args:     args,
		Out:      os.Stdout,
	})
}
