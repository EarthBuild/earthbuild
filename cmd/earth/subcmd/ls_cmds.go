package subcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/earthfile2llb"
	"github.com/EarthBuild/earthbuild/internal/earthfile"
	"github.com/urfave/cli/v3"
)

// List encapsulates the ls command logic.
type List struct {
	cli CLI

	showArgs bool
	showLong bool
}

// NewList creates a new List command.
func NewList(cli CLI) *List {
	return &List{
		cli: cli,
	}
}

// Cmds returns the list of commands for the list command.
func (a *List) Cmds() []*cli.Command {
	return []*cli.Command{
		{
			Name:        "ls",
			Usage:       "List targets from an Earthfile",
			UsageText:   "earth [options] ls [<earthfile-ref>]",
			Description: "List targets from an Earthfile.",
			Action:      a.action,
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:        "args",
					Aliases:     []string{"a"},
					Usage:       "Show Arguments",
					Destination: &a.showArgs,
				},
				&cli.BoolFlag{
					Name:        "long",
					Aliases:     []string{"l"},
					Usage:       "Show full target-ref",
					Destination: &a.showLong,
				},
			},
		},
	}
}

func (a *List) action(ctx context.Context, cmd *cli.Command) error {
	a.cli.SetCommandName("listTargets")

	if cmd.NArg() > 1 {
		return errors.New("invalid number of arguments provided")
	}

	var targetToParse string
	if cmd.NArg() > 0 {
		targetToParse = cmd.Args().Get(0)
		if !strings.HasPrefix(targetToParse, "/") && !strings.HasPrefix(targetToParse, ".") {
			return errors.New("remote-paths are not currently supported; local paths must start with \"/\" or \".\"")
		}

		if strings.Contains(targetToParse, "+") {
			return errors.New("path cannot contain a +")
		}

		targetToParse = strings.TrimSuffix(targetToParse, "/Earthfile")
	}

	targetToDisplay := targetToParse
	if targetToParse == "" {
		targetToDisplay = "current directory"
	}

	// **Read and parsed, not resolved.** Resolving a build context shells out
	// to git for the remote, the hash, the short hash, the branch and the tags -
	// 183ms of this command on this repository's own Earthfile, against about a
	// millisecond to parse the 78 KB it is listing. None of it says anything
	// about what targets a file declares, and remote references are refused a
	// few lines above, so the one thing a resolver is for here cannot happen.
	tree, err := readEarthfile(targetToParse)
	if err != nil {
		return fmt.Errorf("unable to locate Earthfile under %s: %w", targetToDisplay, err)
	}

	targets := earthfile2llb.TargetsIn(tree)

	targets = append(targets, earthfile.TargetBase)
	sort.Strings(targets)

	for _, t := range targets {
		var args []string

		// **Only when somebody asked for them.** `GetTargetArgs` resolves the
		// build context afresh for each target, so this ran a resolution per
		// target and discarded the result unless `--args` was given: 86 targets
		// in this repository's own Earthfile, 0.33s against 0.05s of process
		// startup, for output nobody had asked to see.
		if a.showArgs && t != earthfile.TargetBase {
			args, err = earthfile2llb.TargetArgsIn(tree, t)
			if err != nil {
				return err
			}
		}

		if a.showLong {
			fmt.Printf("%s+%s\n", targetToParse, t)
		} else {
			fmt.Printf("+%s\n", t)
		}

		if a.showArgs {
			for _, arg := range args {
				fmt.Printf("  --%s\n", arg)
			}
		}
	}

	return nil
}

// readEarthfile parses the Earthfile of a directory, defaulting to this one.
//
// The path is in the error because "no Earthfile" is a question about *where*:
// the answer is almost always that the directory is not the one the author
// meant.
func readEarthfile(dir string) (earthfile.Tree, error) {
	if dir == "" {
		dir = "."
	}

	path := filepath.Join(dir, "Earthfile")

	src, err := os.ReadFile(path) //nolint:gosec // the directory the caller named
	if err != nil {
		return earthfile.Tree{}, fmt.Errorf("looked for %s", path)
	}

	tree, err := earthfile.Parse(path, string(src), earthfile.WithSourceMap())
	if err != nil {
		return earthfile.Tree{}, fmt.Errorf("parse %s: %w", path, err)
	}

	return tree, nil
}
