package base

import (
	"context"
	"fmt"

	"github.com/EarthBuild/earthbuild/buildkitd"
	"github.com/moby/buildkit/client"
	"github.com/urfave/cli/v3"
)

// GetBuildkitClient returns a Buildkit client.
func (cli *CLI) GetBuildkitClient(ctx context.Context, cmd *cli.Command) (c *client.Client, err error) {
	err = cli.InitContainer(cmd)
	if err != nil {
		return nil, err
	}

	c, err = buildkitd.NewClient(
		ctx, cli.Log(), cli.Flags().BuildkitdImage, cli.Flags().ContainerName, cli.Flags().InstallationName,
		cli.Flags().Engine, cli.Version(), cli.Flags().BuildkitdSettings,
	)
	if err != nil {
		return nil, fmt.Errorf("could not construct new buildkit client: %w", err)
	}

	return c, nil
}
