package subcmd

import (
	"context"
	"fmt"
	"os"

	"github.com/EarthBuild/earthbuild/buildcontext"
	"github.com/EarthBuild/earthbuild/docker2earth"
	"github.com/urfave/cli/v3"
)

// Doc2Earth encapsulates the doc2earth command logic.
type Doc2Earth struct {
	cli CLI

	earthfilePath       string
	earthfileFinalImage string
}

// NewDoc2Earth creates a new Doc2Earth command.
func NewDoc2Earth(cli CLI) *Doc2Earth {
	return &Doc2Earth{
		cli: cli,
	}
}

const (
	// docker2EarthCmdName is the name of the command that converts a Dockerfile into an Earthfile.
	docker2EarthCmdName = "docker2earth"
	// deprecatedDocker2EarthCmdName is the pre-rename name of the command, kept as an alias for
	// backwards compatibility.
	deprecatedDocker2EarthCmdName = "docker2earthly"
)

// Cmds returns the list of commands for the docker2earth command.
func (a *Doc2Earth) Cmds() []*cli.Command {
	return []*cli.Command{
		{
			Name:        docker2EarthCmdName,
			Aliases:     []string{deprecatedDocker2EarthCmdName},
			Usage:       "Convert a Dockerfile into Earthfile",
			Description: "Converts an existing dockerfile into an Earthfile.",
			Hidden:      true, // Experimental.
			Action:      a.action,
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:        "dockerfile",
					Usage:       "Path to dockerfile input, or - for stdin",
					Value:       "Dockerfile",
					Destination: &a.cli.Flags().DockerfilePath,
				},
				&cli.StringFlag{
					Name:        "earthfile",
					Usage:       "Path to Earthfile output, or - for stdout",
					Value:       buildcontext.Earthfile,
					Destination: &a.earthfilePath,
				},
				&cli.StringFlag{
					Name:        "tag",
					Usage:       "Name and tag for the built image; formatted as 'name:tag'",
					Destination: &a.earthfileFinalImage,
				},
			},
		},
	}
}

func (a *Doc2Earth) action(_ context.Context, cmd *cli.Command) error {
	a.cli.SetCommandName(docker2EarthCmdName)

	if invokedAs(cmd) == deprecatedDocker2EarthCmdName {
		a.cli.Log().Warnf(
			"WARNING: %s command is deprecated and will be removed soon. Use %s instead.\n",
			deprecatedDocker2EarthCmdName, docker2EarthCmdName)
	}

	err := docker2earth.Docker2Earth(a.cli.Flags().DockerfilePath, a.earthfilePath, a.earthfileFinalImage)
	if err != nil {
		return err
	}

	format := "An Earthfile has been generated; to run it use: earth +build; then run with docker run -ti %s\n"
	fmt.Fprintf(os.Stderr, format, a.earthfileFinalImage)

	return nil
}

// invokedAs returns the name (or alias) the user typed to invoke cmd. The
// parent command dispatches to a sub-command using the first of its own
// arguments, so that argument is the name as typed. It falls back to cmd.Name
// when cmd has no parent.
func invokedAs(cmd *cli.Command) string {
	lineage := cmd.Lineage()
	if len(lineage) < 2 {
		return cmd.Name
	}

	if name := lineage[1].Args().First(); name != "" {
		return name
	}

	return cmd.Name
}
