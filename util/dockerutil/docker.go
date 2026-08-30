package dockerutil

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/util/containerutil"
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
	log *conslogging.ConsoleLogger,
	fe containerutil.ContainerFrontend,
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
		log.Warnf(
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
	log.Printf(
		"Image %s is a multi-platform image. The following per-platform images have been produced:\n\t%s\n%s\n",
		parentImageName, strings.Join(childImgs, "\n\t"), noteDetail,
	)

	err := fe.ImageTag(ctx, containerutil.ImageTag{
		SourceRef: children[defaultChild].ImageName,
		TargetRef: parentImageName,
	})
	if err != nil {
		return fmt.Errorf("docker tag default platform image: %w", err)
	}

	return nil
}

// LoadDockerTar loads a docker image via a tar.
func LoadDockerTar(ctx context.Context, fe containerutil.ContainerFrontend, r io.ReadCloser) error {
	err := fe.ImageLoad(ctx, r)
	if err != nil {
		return fmt.Errorf("load tar: %w", err)
	}

	return nil
}

// DockerPullLocalImages pulls a docker image from a local registry.
func DockerPullLocalImages(
	ctx context.Context,
	fe containerutil.ContainerFrontend,
	localRegistryAddr string,
	pullMap map[string]string,
) error {
	eg, ctx := errgroup.WithContext(ctx)

	for pullName, finalName := range pullMap {
		pn := pullName
		fn := finalName

		eg.Go(func() error {
			return dockerPullLocalImage(ctx, fe, localRegistryAddr, pn, fn)
		})
	}

	return eg.Wait()
}

// pullAttempts is how many times a pull from the local registry is tried, and
// pullRetryBase the wait before the second try. Three attempts and 150ms cost a
// broken frontend under half a second and cover the fault below comfortably.
const (
	pullAttempts  = 3
	pullRetryBase = 150 * time.Millisecond
)

// pullWithRetry pulls from the session-scoped local registry, retrying briefly.
//
// **The failure this exists for is a closed connection, not an answer.** The
// image is one buildkitd has just published to a registry on loopback, so it is
// there; what CI produces about once in a hundred job-runs is
//
//	failed to copy: httpReadSeeker: failed open: failed to do request:
//	  Get "https://127.0.0.1:PORT/v2/sess-ID/pullping/blobs/sha256:...": EOF
//
// a bare EOF with no status, which is what a server closing an idle keep-alive
// connection under a client that is about to reuse it looks like from the
// client's end. Retrying is the client half of that race; the server half lives
// in buildkit's session registry.
//
// **Every error is retried, not a matched subset.** The alternative is deciding
// which failures are transient by matching text in a subprocess's stderr, which
// makes this code depend on the wording of another program's messages. It is
// not needed here: the ref names an image that exists on a registry this
// process is talking to over loopback, so a pull that fails three times in half
// a second has something wrong with it that a fourth would not fix, and one
// that fails permanently fails just as loudly half a second later.
func pullWithRetry(ctx context.Context, fe containerutil.ContainerFrontend, ref string) error {
	var err error

	for attempt := 1; attempt <= pullAttempts; attempt++ {
		err = fe.ImagePull(ctx, ref)
		if err == nil {
			return nil
		}

		if attempt == pullAttempts {
			break
		}

		// Doubling, from a base small enough that a build which never sees this
		// fault does not notice the code exists.
		wait := pullRetryBase * time.Duration(1<<(attempt-1))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}

	return err
}

func dockerPullLocalImage(
	ctx context.Context, fe containerutil.ContainerFrontend, localRegistryAddr, pullName, finalName string,
) error {
	fullPullName := fmt.Sprintf("%s/%s", localRegistryAddr, pullName)

	err := pullWithRetry(ctx, fe, fullPullName)
	if err != nil {
		return fmt.Errorf("image pull: %w", err)
	}

	// Fix for #2471 where Podman pulls seem exit before the image is available
	// for tagging. Wait for the image to become available.
	err = waitForImage(ctx, fe, fullPullName)
	if err != nil {
		return err
	}

	err = fe.ImageTag(ctx, containerutil.ImageTag{
		SourceRef: fullPullName,
		TargetRef: finalName,
	})
	if err != nil {
		return fmt.Errorf("image tag after pull: %w", err)
	}

	force := true // Sometimes Docker GCs images automatically (force prevents an error).

	err = fe.ImageRemove(ctx, force, fullPullName)
	if err != nil {
		return fmt.Errorf("image rmi after pull and retag: %w", err)
	}

	return nil
}

func waitForImage(ctx context.Context, fe containerutil.ContainerFrontend, fullName string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			m, err := fe.ImageInfo(ctx, fullName)
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
