package subcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"

	"github.com/EarthBuild/earthbuild/cmd/earth/common"
	"github.com/EarthBuild/earthbuild/cmd/earth/flag"
	"github.com/EarthBuild/earthbuild/domain"
	enginecli "github.com/EarthBuild/earthbuild/engine/cli"
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
// nativeSecrets is what `--secret`, `--secret-file` and the secrets dotenv file
// say, in the form the engine takes.
//
// **Merged here rather than handed to the engine in pieces, on purpose.**
// `earth-native` passes `Secrets`, `SecretFiles` and `SecretFile` separately and
// lets the engine layer them, preferring `--secret` over a named file over
// `.secret`. `common.ProcessSecrets` instead *refuses* a key that appears in two
// places. Both are defensible; this path takes the stricter one because it is
// what `--engine=buildkit` does, and the same command line should not change the
// precedence of a credential according to which engine runs it. The looser
// layering stays available through `earth-native`, which is a developer's
// front-end to the same engine.
//
// **A copy of the buildkit path's three lines, on purpose.** The native branch
// returns before that path prepares anything, deliberately - it brings its own
// scheduling, store and sandbox, and sharing the preparation would start a
// daemon neither engine uses. Moving the shared preparation earlier to reach it
// would change which error a bad invocation reports first for buildkit builds,
// for the benefit of a branch that returns immediately afterwards.
func (b *Build) nativeSecrets(cmd *cli.Command) (map[string]string, error) {
	fromFile, err := godotenv.Read(b.cli.Flags().SecretFile)
	if err != nil && (cmd.IsSet(flag.SecretFileFlag) || !errors.Is(err, os.ErrNotExist)) {
		// A default `.secret` that is not there is not an error; one that was
		// asked for by name is.
		return nil, fmt.Errorf("read %s: %w", b.cli.Flags().SecretFile, err)
	}

	raw, err := common.ProcessSecrets(
		b.secrets, b.secretFiles, fromFile, b.cli.Flags().SecretFile)
	if err != nil {
		return nil, err
	}

	// The engine holds secrets as strings; `ProcessSecrets` returns bytes.
	// Ranged rather than length-checked, so `--secret FOO=` - a secret that
	// exists and is empty - arrives as a key rather than being dropped.
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = string(v)
	}

	return out, nil
}

func (b *Build) runNative(
	ctx context.Context, cmd *cli.Command, target domain.Target, flagArgs []string,
) error {
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

	secrets, err := b.nativeSecrets(cmd)
	if err != nil {
		return err
	}

	// **Only when the caller named one.** `namedFile` treats any non-empty path
	// as named and insists it exists, so handing it the flag's own default
	// turned every build without a `.arg` into `open .arg: no such file or
	// directory`. The buildkit path never hits this because it reads the file
	// itself and tolerates a missing default.
	argFile := ""
	if cmd.IsSet(flag.ArgFileFlag) {
		argFile = b.cli.Flags().ArgFile
	}

	return enginecli.Run(ctx, nativeOptions(nativeInput{
		dir:             dir,
		target:          target.Target,
		platform:        platform,
		args:            args,
		secrets:         secrets,
		allowPrivileged: b.cli.Flags().AllowPrivileged,
		noCache:         b.cli.Flags().NoCache,
		push:            b.cli.Flags().Push,
		noOutput:        b.cli.Flags().NoOutput,
		execStats:       b.cli.Flags().DisplayExecStats,
		versionFlags:    splitCommaList(b.cli.Flags().FeatureFlagOverrides),
		argFile:         argFile,
	}))
}

// nativeInput is what the command line said, in the terms this engine takes.
//
// A struct and a function rather than a literal inline, so a test can assert
// that a flag *arrives*. The bug this exists to prevent is the one the build
// arguments already had and `--allow-privileged` then repeated: a flag parsed
// into the globals, never copied into the options, and the engine refusing on a
// permission the operator had granted. Eleven of fifteen Native CI jobs failed
// on it, and read as a policy decision rather than a dropped field.
type nativeInput struct {
	dir             string
	target          string
	platform        string
	args            map[string]string
	secrets         map[string]string
	allowPrivileged bool
	noCache         bool
	push            bool
	noOutput        bool
	execStats       bool
	argFile         string
	versionFlags    []string
}

// nativeOptions is the whole of the translation, in one place that can be read.
func nativeOptions(in nativeInput) enginecli.Options {
	return enginecli.Options{
		Dir:    in.dir,
		Target: "+" + in.target,
		// One platform, because this engine builds for one at a time: the
		// reference takes a list and fans out, and taking the first of a list
		// silently would build something the caller did not ask for.
		Platform:        in.platform,
		Args:            in.args,
		Secrets:         in.secrets,
		AllowPrivileged: in.allowPrivileged,
		NoCache:         in.noCache,
		Push:            in.push,
		NoOutput:        in.noOutput,
		ExecStats:       in.execStats,
		ArgFile:         in.argFile,
		VersionFlags:    in.versionFlags,
		Out:             os.Stdout,
	}
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
			// **A bare name means the environment**, which is what the other
			// backend has always done and what this repository's own workflow
			// passes: `--build-arg TAG_SUFFIX +ci-release`, with the value
			// exported by the job. The two engines are chosen by a flag, so a
			// command line one accepts and the other refuses is a build that
			// works until somebody switches.
			value, ok = os.LookupEnv(name)
			if !ok {
				// Not defaulted to empty: an empty string is a value a build can
				// legitimately be given, so guessing one would make "you forgot
				// to export it" and "you meant it to be empty" the same command.
				return nil, fmt.Errorf(
					"--engine=%s: build argument %q has no value and %s is not"+
						" set in the environment"+
						"\n  write it as %s=<value>, or export %s before building",
					nativeEngine, a, name, a, name)
			}
		}

		args[name] = value
	}

	return args, nil
}

// splitCommaList turns `--version-flag-overrides` into the list the engine
// takes, matching earth-native's `splitList`: empty means none, and each entry
// is trimmed, so `a, b` and `a,b` mean the same thing.
func splitCommaList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}

	out := strings.Split(v, ",")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}

	return out
}
