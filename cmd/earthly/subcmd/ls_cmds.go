package subcmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/EarthBuild/earthbuild/ast"
	"github.com/EarthBuild/earthbuild/buildcontext"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/earthfile2llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"

	"github.com/urfave/cli/v2"
)

type List struct {
	cli CLI

	showArgs bool
	showLong bool
}

func NewList(cli CLI) *List {
	return &List{
		cli: cli,
	}
}

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

func (a *List) action(cliCtx *cli.Context) error {
	a.cli.SetCommandName("listTargets")

	if cliCtx.NArg() > 1 {
		return errors.New("invalid number of arguments provided")
	}

	var targetToParse string
	if cliCtx.NArg() > 0 {
		targetToParse = cliCtx.Args().Get(0)
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

	gitLookup := buildcontext.NewGitLookup(a.cli.Console(), a.cli.Flags().SSHAuthSock)
	resolver := buildcontext.NewResolver(
		nil, gitLookup, a.cli.Console(), "", a.cli.Flags().GitBranchOverride, a.cli.Flags().GitLFSPullInclude, 0, "")

	// TODO this is a nil pointer which causes a panic if we try to expand a remotelyreferenced earthfile
	// it's expensive to create this gwclient, so we need to implement a lazy eval which returns it when required.
	var (
		gwClient    gwclient.Client
		notExistErr buildcontext.EarthfileNotExistError
	)

	// the +base is required to make ParseTarget work; however is ignored by GetTargets
	target, err := domain.ParseTarget(targetToParse + "+base")
	if errors.As(err, &notExistErr) {
		return errors.Errorf("unable to locate Earthfile under %s", targetToDisplay)
	} else if err != nil {
		return err
	}

	targets, err := earthfile2llb.GetTargets(cliCtx.Context, resolver, gwClient, target)
	if err != nil {
		if errors.As(errors.Cause(err), &notExistErr) {
			return errors.Errorf("unable to locate Earthfile under %s", targetToDisplay)
		}

		return err
	}

	targets = append(targets, ast.TargetBase)
	sort.Strings(targets)

	for _, t := range targets {
		var args []string

		if t != ast.TargetBase {
			target.Target = t

			args, err = earthfile2llb.GetTargetArgs(cliCtx.Context, resolver, gwClient, target)
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
