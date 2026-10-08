package platutil

import (
	"context"
	"errors"
	"fmt"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// GetNativePlatformViaBkClient returns the native platform for a given buildkit client.
func GetNativePlatformViaBkClient(ctx context.Context, bkClient *client.Client) (specs.Platform, error) {
	ws, err := bkClient.ListWorkers(ctx)
	if err != nil {
		return specs.Platform{}, fmt.Errorf("failed to list workers: %w", err)
	}

	if len(ws) == 0 {
		return specs.Platform{}, errors.New("no worker found via bkClient")
	}

	nps := ws[0].Platforms
	if len(nps) == 0 {
		return specs.Platform{}, errors.New("no platform found for worker via bkClient")
	}

	return platforms.Normalize(nps[0]), nil
}
