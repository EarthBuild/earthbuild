package subcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/EarthBuild/earthbuild/buildcontext"
	"github.com/EarthBuild/earthbuild/internal/dockerfile"
	"github.com/EarthBuild/earthbuild/util/fileutil"
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

// Cmds returns the list of commands for the docker2earth command.
func (a *Doc2Earth) Cmds() []*cli.Command {
	return []*cli.Command{
		{
			Name:        "docker2earthly",
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

func (a *Doc2Earth) action(context.Context, *cli.Command) error {
	a.cli.SetCommandName("docker2earthly")

	var in io.Reader
	if a.cli.Flags().DockerfilePath == "-" {
		in = os.Stdin
	} else {
		path := filepath.Clean(a.cli.Flags().DockerfilePath)

		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %q: %w", path, err)
		}

		defer f.Close()

		in = f
	}

	var out io.Writer
	if a.earthfilePath == "-" {
		out = os.Stdout
	} else {
		path := filepath.Clean(a.earthfilePath)

		if exists, _ := fileutil.FileExists(path); exists {
			return errors.New("earthfile already exists; please delete it if you wish to continue")
		}

		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create Earthfile %q: %w", path, err)
		}

		defer f.Close()

		out = f
	}

	_, err := dockerfile.Convert(out, in, "earth docker2earth", a.earthfileFinalImage, a.cli.Flags().DockerfilePath)
	if err != nil {
		return err
	}

	format := "An Earthfile has been generated; to run it use: earth +build; then run with docker run -ti %s\n"
	fmt.Fprintf(os.Stderr, format, a.earthfileFinalImage)

	return nil
}
