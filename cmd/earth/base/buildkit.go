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

	cli.Flags().LocalRegistryHost = localRegistryHost(
		ctx, cli.Flags().Engine, cli.Flags().ContainerName, cli.Flags().LocalRegistryHost,
	)

	return c, nil
}

// localRegistryHost returns the address of the managed buildkitd's local registry. Docker and
// Podman publish it on the configured host port, so that address stands: swapping in the
// container port drifts the settings hash and restarts buildkitd onto the default port. Apple
// Container publishes no host ports, so only there is the registry reached at the container IP.
func localRegistryHost(ctx context.Context, eng *engine.Client, containerName, configured string) string {
	if configured == "" || !engine.IsLocal(configured) || eng.Metadata().Scheme != engine.SchemeApple {
		return configured
	}

	addr, err := eng.ContainerAddr(ctx, containerName, engine.DefaultLocalRegistryPort)
	if err != nil || addr == "" {
		return configured
	}

	return addr
}
