package subcmd

import (
	"cmp"
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

	// Parsed rather than resolved: resolving runs git for the remote, hash,
	// branch and tags, and remote references are refused above.
	path := filepath.Join(cmp.Or(targetToParse, "."), "Earthfile")

	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("unable to locate Earthfile under %s", targetToDisplay)
	}

	ef, err := earthfile.Parse(path, string(src), earthfile.WithSourceMap())
	if err != nil {
		return err
	}

	targets := make([]string, 0, len(ef.Targets))
	for _, t := range ef.Targets {
		targets = append(targets, t.Name)
	}

	targets = append(targets, earthfile.TargetBase)
	sort.Strings(targets)

	for _, t := range targets {
		var args []string

		if a.showArgs && t != earthfile.TargetBase {
			args, err = earthfile2llb.TargetArgs(ef, t)
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
