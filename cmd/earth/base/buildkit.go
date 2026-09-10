package base

import (
	"context"
	"errors"
	"fmt"

	"github.com/EarthBuild/earthbuild/buildkitd"
	"github.com/EarthBuild/earthbuild/internal/engine"
	"github.com/moby/buildkit/client"
	"github.com/urfave/cli/v3"
)

// GetBuildkitClient returns a Buildkit client.
func (cli *CLI) GetBuildkitClient(ctx context.Context, cmd *cli.Command) (*client.Client, error) {
	err := cli.InitBuildkit(cmd)
	if err != nil {
		return nil, fmt.Errorf("init buildkit: %w", err)
	}

	if cli.Flags().BuildkitdSettings.BuildkitAddr == "" {
		return nil, errors.New(
			"could not determine buildkit address - is Docker, Podman, or Apple Container running?",
		)
	}

	c, err := buildkitd.NewClient(
		ctx, cli.Log(), cli.Flags().BuildkitdImage, cli.Flags().ContainerName,
		cli.Flags().Engine, cli.Version(), cli.Flags().BuildkitdSettings,
	)
	if err != nil {
		return nil, fmt.Errorf("new buildkit client: %w", err)
	}

	if cli.Flags().LocalRegistryHost != "" && engine.IsLocal(cli.Flags().LocalRegistryHost) {
		addr, err := cli.Flags().Engine.ContainerAddr(ctx, cli.Flags().ContainerName, 8371)
		if err == nil && addr != "" {
			cli.Flags().LocalRegistryHost = addr
		}
	}

	return c, nil
}
