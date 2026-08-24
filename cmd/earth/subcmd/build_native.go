package subcmd

import (
	"context"
	"fmt"
	"os"
	"slices"
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

	args, err := nativeArgs(flagArgs, b.buildArgs)
	if err != nil {
		return err
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

// nativeArgs is the build arguments a native build starts with.
//
// **Two spellings, both of which must arrive.** `--build-arg NAME=VALUE` lands
// in `b.buildArgs`; a trailing `+target --NAME=VALUE` lands in `flagArgs`.
// Buildkit's path combines them in `common.CombineVariables` and this one read
// only the second, so `--build-arg` was parsed, stored and never looked at: the
// build ran on the Earthfile's default and reported nothing amiss (E611).
//
// Order matches buildkit's `slices.Concat(buildFlagArgs, flagArgs)` - the
// trailing form overrides the flag - because two engines that disagree about
// precedence are worse than either rule alone.
//
// `NAME=VALUE`, and a bare name is refused rather than guessed at: taking the
// shell's value, as the reference does, makes a build that silently used
// something else, which is worse than one that stops.
func nativeArgs(flagArgs, buildArgs []string) (map[string]string, error) {
	args := map[string]string{}

	for _, a := range slices.Concat(buildArgs, flagArgs) {
		name, value, ok := strings.Cut(a, "=")
		if !ok {
			return nil, fmt.Errorf(
				"--engine=%s: build argument %q has no value"+
					"\n  write it as %s=<value>", nativeEngine, a, a)
		}

		args[name] = value
	}

	return args, nil
}
