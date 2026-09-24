package subcmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/EarthBuild/earthbuild/variables"

	enginecli "github.com/EarthBuild/earthbuild/engine/cli"
)

// inputCmds are `emit-inputs` and `check-inputs`: what a build reads, written
// down, and a later checkout compared against it.
//
// **Commands rather than flags on `build`, because neither builds.** A flag that
// silently turns a build into a question is the sort of thing somebody adds to
// CI and then spends an afternoon wondering why nothing was produced. The name
// says what happens.
//
// They carry the whole build flag set - `--build-arg`, `--platform`, `--secret`
// and the rest - because those decide the plan and therefore the fingerprint. A
// check run with different arguments from the emit that preceded it is a
// different question, and answering it as though it were the same one is exactly
// the false green this exists to avoid.
func (b *Build) inputCmds() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "emit-inputs",
			Usage: "Write down what a target's build reads, and run nothing",
			UsageText: "earth [options] emit-inputs <file> <target> " +
				"[--build-arg <name>=<value>]",
			Description: "Write down what a target's build reads, and run nothing.",
			Action:      b.actionEmitInputs,
			Flags:       b.buildFlags(),
		},
		{
			Name:  "check-inputs",
			Usage: "Ask whether a target's inputs have changed, and run nothing",
			UsageText: "earth [options] check-inputs <file> <target> " +
				"[--build-arg <name>=<value>]",
			Description: "Ask whether a target's inputs have changed, and run nothing. " +
				"Exits 0 unchanged, 2 changed, 1 on a failure.",
			Action: b.actionCheckInputs,
			Flags:  b.buildFlags(),
		},
	}
}

func (b *Build) actionEmitInputs(ctx context.Context, cmd *cli.Command) error {
	return b.actionInputs(ctx, cmd, "emit-inputs")
}

func (b *Build) actionCheckInputs(ctx context.Context, cmd *cli.Command) error {
	return b.actionInputs(ctx, cmd, "check-inputs")
}

// actionInputs is both commands: the file, then the target, then the native
// build path with nothing to run.
func (b *Build) actionInputs(ctx context.Context, cmd *cli.Command, name string) error {
	b.cli.SetCommandName(name)

	// **Only the native engine has a plan to fingerprint.** Buildkit's answer to
	// the same question is `--auto-skip`, which keeps its keys in a database
	// rather than in a file a CI cache can carry - a different mechanism with a
	// different home, and quietly substituting one for the other would answer a
	// question nobody asked.
	if engine := b.cli.Flags().Engine; engine != "" && engine != nativeEngine {
		return fmt.Errorf(
			"%s is a question about the native engine's plan, and --engine=%s was asked for"+
				"\n  use --engine=%s, or --auto-skip for the buildkit equivalent",
			name, engine, nativeEngine)
	}

	flagArgs, nonFlagArgs, err := variables.ParseFlagArgsWithNonFlags(cmd.Args().Slice())
	if err != nil {
		return fmt.Errorf("parse args %s: %w", strings.Join(cmd.Args().Slice(), " "), err)
	}

	if len(nonFlagArgs) < 2 {
		return fmt.Errorf("%s takes a file and a target"+
			"\n  earth %s inputs.json +test", name, name)
	}

	at, rest := nonFlagArgs[0], nonFlagArgs[1:]

	target, artifact, destPath, err := b.parseTarget(cmd, rest)
	if err != nil {
		return err
	}

	if artifact.Target.Target != "" || destPath != "./" {
		return fmt.Errorf("%s is a question about a target, and this invocation names an artifact",
			name)
	}

	opts, err := b.nativeOptionsFor(cmd, target, flagArgs)
	if err != nil {
		return err
	}

	if name == "emit-inputs" {
		opts.EmitInputs = at
	} else {
		opts.CheckInputs = at
	}

	return enginecli.Run(ctx, opts)
}

// unchangedExitCode is what a caller reads to decide whether to run the job.
//
// Distinct from 1, which is every other failure: a caller that cannot tell
// "changed" from "the Earthfile does not parse" skips the job on a broken
// build, which is the one outcome a skip must never be.
const unchangedExitCode = 2

// InputsChangedCode is the exit code for a build that must run, or 0 for any
// other error.
func InputsChangedCode(err error) int {
	if errors.Is(err, enginecli.ErrInputsChanged) {
		return unchangedExitCode
	}

	return 0
}
