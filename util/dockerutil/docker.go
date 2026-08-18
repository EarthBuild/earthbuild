package dockerutil

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/container"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"golang.org/x/sync/errgroup"
)

// Manifest contains docker manifest data.
type Manifest struct {
	Platform  platutil.Platform
	ImageName string
}

// LoadDockerManifest loads docker manifests.
func LoadDockerManifest(
	ctx context.Context,
	console conslogging.ConsoleLogger,
	containerClient *container.Client,
	parentImageName string,
	children []Manifest,
	platr *platutil.Resolver,
) error {
	if len(children) == 0 {
		return fmt.Errorf("no images in manifest list for %s", parentImageName)
	}
	// Check if any child has the platform as the default platform
	defaultChild := 0
	foundPlatform := false

	for i, child := range children {
		if platr.PlatformEquals(child.Platform, platutil.DefaultPlatform) {
			defaultChild = i
			foundPlatform = true

			break
		}
	}

	if !foundPlatform {
		// fall back to using first defined platform (and display a warning)
		console.Warnf(
			"Failed to find default platform (%s) of multi-platform image %s; defaulting to the first platform type: %s\n",
			platr.Materialize(platutil.DefaultPlatform).String(), parentImageName, children[defaultChild].Platform,
		)
	}

	var childImgs []string

	for i, child := range children {
		if i == defaultChild {
			childImgs = append(childImgs, fmt.Sprintf("%s (=%s)", child.ImageName, parentImageName))
		} else {
			childImgs = append(childImgs, child.ImageName)
		}
	}

	const noteDetail = "Note that when pushing a multi-platform image, " +
		"it is pushed as a single multi-manifest image. " +
		"Separate per-platform image tags are only available locally."
	console.Printf(
		"Image %s is a multi-platform image. The following per-platform images have been produced:\n\t%s\n%s\n",
		parentImageName, strings.Join(childImgs, "\n\t"), noteDetail,
	)

	err := containerClient.ImageTag(ctx, container.Tag{
		SourceRef: children[defaultChild].ImageName,
		TargetRef: parentImageName,
	})
	if err != nil {
		return fmt.Errorf("docker tag default platform image: %w", err)
	}

	return nil
}

// LoadDockerTar loads a docker image via a tar.
func LoadDockerTar(ctx context.Context, containerClient *container.Client, r io.ReadCloser) error {
	err := containerClient.ImageLoad(ctx, r)
	if err != nil {
		return fmt.Errorf("load tar: %w", err)
	}

	return nil
}

// DockerPullLocalImages pulls a docker image from a local registry.
func DockerPullLocalImages(
	ctx context.Context,
	containerClient *container.Client,
	localRegistryAddr string,
	pullMap map[string]string,
) error {
	eg, ctx := errgroup.WithContext(ctx)

	for pullName, finalName := range pullMap {
		pn := pullName
		fn := finalName

		eg.Go(func() error {
			return dockerPullLocalImage(ctx, containerClient, localRegistryAddr, pn, fn)
		})
	}

	return eg.Wait()
}

func dockerPullLocalImage(
	ctx context.Context, containerClient *container.Client, localRegistryAddr, pullName, finalName string,
) error {
	fullPullName := fmt.Sprintf("%s/%s", localRegistryAddr, pullName)

	err := containerClient.ImagePull(ctx, fullPullName)
	if err != nil {
		return fmt.Errorf("image pull: %w", err)
	}

	// Fix for #2471 where Podman pulls seem exit before the image is available
	// for tagging. Wait for the image to become available.
	err = waitForImage(ctx, containerClient, fullPullName)
	if err != nil {
		return err
	}

	err = containerClient.ImageTag(ctx, container.Tag{
		SourceRef: fullPullName,
		TargetRef: finalName,
	})
	if err != nil {
		return fmt.Errorf("image tag after pull: %w", err)
	}

	force := true // Sometimes Docker GCs images automatically (force prevents an error).

	err = containerClient.ImageRemove(ctx, force, fullPullName)
	if err != nil {
		return fmt.Errorf("image rmi after pull and retag: %w", err)
	}

	return nil
}

func waitForImage(ctx context.Context, containerClient *container.Client, fullName string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			m, err := containerClient.ImageInfo(ctx, fullName)
			if err != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
					continue // Not available. Retry.
				}
			}

			if info, ok := m[fullName]; ok && info.ID != "" {
				return nil
			}
		}
	}
}
