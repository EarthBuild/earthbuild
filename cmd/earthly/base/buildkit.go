package base

import (
	"context"

	"github.com/EarthBuild/earthbuild/buildkitd"
	"github.com/moby/buildkit/client"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v3"
)

func (cli *CLI) GetBuildkitClient(ctx context.Context, cmd *cli.Command) (c *client.Client, err error) {
	err = cli.InitFrontend(ctx, cmd)
	if err != nil {
		return nil, err
	}

	c, err = buildkitd.NewClient(
		ctx, cli.Console(), cli.Flags().BuildkitdImage, cli.Flags().ContainerName, cli.Flags().InstallationName,
		cli.Flags().ContainerFrontend, cli.Version(), cli.Flags().BuildkitdSettings)
	if err != nil {
		return nil, errors.Wrap(err, "could not construct new buildkit client")
	}

	return c, nil
}
