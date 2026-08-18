package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/dustin/go-humanize"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // Load "docker-container://" helper.
)

// dockerEngine implements Engine for the Docker CLI.
type dockerEngine struct {
	*shellEngine

	userNamespaced bool
	isPodman       bool
}

// newDockerEngine constructs a new Engine using the docker binary installed on the host.
func newDockerEngine(ctx context.Context, cfg *Config) (engineDriver, error) {
	e := &dockerEngine{
		shellEngine: &shellEngine{
			BinaryName:              string(Docker),
			RunCompatibilityArgs:    make([]string, 0),
			GlobalCompatibilityArgs: make([]string, 0),
			Console:                 cfg.Console,
		},
	}

	// running `docker info --format={{.SecurityOptions}}` results in a panic() when docker is not running.
	// To workaround this issue, first we run `docker info` to test docker is running, then again with the
	// `--format` option.
	_, err := e.CommandOutput(ctx, "info")
	if err != nil {
		return nil, err
	}

	output, err := e.CommandOutput(ctx, "info", "--format={{.SecurityOptions}}")
	if err != nil {
		return nil, err
	}

	e.Rootless = strings.Contains(output.String(), "rootless")

	e.userNamespaced = strings.Contains(output.String(), "name=userns")
	if e.userNamespaced {
		e.RunCompatibilityArgs = []string{"--userns", "host"}
	}

	e.Endpoints, err = e.ResolveEndpoints(DockerShell, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate buildkit URLs: %w", err)
	}

	output, err = e.CommandOutput(ctx, "info", "--format={{.DockerRootDir}}")
	if err != nil {
		// Maybe the user has aliased podman=docker?
		var err2 error

		output, err2 = e.CommandOutput(ctx, "info", "--format={{.Store.GraphRoot}}")
		if err2 != nil {
			return nil, fmt.Errorf("failed to get docker root dir: %w", err)
		}
	}

	outputStr := strings.TrimSpace(output.String())
	if outputStr == "/var/lib/containers/storage" {
		// Likely podman making itself available via the docker CLI.
		e.isPodman = true
	}

	return e, nil
}

// Metadata returns current engine metadata.
func (e *dockerEngine) Metadata() Metadata {
	return Metadata{
		Name:      "Docker",
		Scheme:    SchemeDocker,
		Binary:    e.BinaryName,
		Transport: TransportShell,
		Endpoints: e.Endpoints,
		IsPodman:  e.isPodman,
	}
}

// Version returns version and platform information for the Docker CLI and daemon.
func (e *dockerEngine) Version(ctx context.Context) (Version, error) {
	output, err := e.CommandOutput(ctx, "version", "--format={{json .}}")
	if err != nil {
		return Version{}, err
	}

	type versionInfo struct {
		Version    string
		APIVersion string
		OS         string
		Arch       string
	}

	type info struct {
		Client versionInfo
		Server versionInfo
	}

	allInfo := info{}

	err = json.Unmarshal([]byte(output.String()), &allInfo)
	if err != nil {
		return Version{}, fmt.Errorf("failed to parse docker version output: %w", err)
	}

	host, exists := os.LookupEnv("DOCKER_HOST")
	if !exists {
		host = "/var/run/docker.sock"
	}

	return Version{
		ClientVersion:    allInfo.Client.Version,
		ClientAPIVersion: allInfo.Client.APIVersion,
		ClientPlatform:   fmt.Sprintf("%s/%s", allInfo.Client.OS, allInfo.Client.Arch),
		ServerVersion:    allInfo.Server.Version,
		ServerAPIVersion: allInfo.Server.APIVersion,
		ServerPlatform:   fmt.Sprintf("%s/%s", allInfo.Server.OS, allInfo.Server.Arch),
		ServerAddress:    host,
	}, nil
}

// InspectContainer returns information for the given container names or IDs.
func (e *dockerEngine) InspectContainer(
	ctx context.Context, namesOrIDs ...string,
) (map[string]Container, error) {
	results, err := e.shellEngine.InspectContainer(ctx, namesOrIDs...)
	if err != nil {
		return nil, err
	}

	for k, v := range results {
		// Docker prepends a `/`. This is as intended, according to docker; but unexpected in our
		// case. So remove it. If the status is missing, it was passed through so do not remove.
		if v.Status != StatusMissing {
			if name, ok := strings.CutPrefix(v.Name, "/"); ok {
				v.Name = name
				results[k] = v
			}
		}
	}

	return results, nil
}

// PullImage downloads the specified container images.
func (e *dockerEngine) PullImage(ctx context.Context, refs ...string) error {
	var err error

	for _, ref := range refs {
		_, cmdErr := e.CommandOutput(ctx, "pull", ref)
		if cmdErr != nil {
			err = errors.Join(err, cmdErr)
		}
	}

	return err
}

// ImageLoadCommand returns the shell command to load an image from a file.
func (e *dockerEngine) ImageLoadCommand(filename string) string {
	return fmt.Sprintf("cat %s | %s", shellescape.Quote(filename), strings.Join(e.CommandArgs("load"), " "))
}

// LoadImage loads images into Docker via stdin.
func (e *dockerEngine) LoadImage(ctx context.Context, images ...io.Reader) error {
	var err error

	for _, image := range images {
		// Do not use the wrapper to allow the image to come in on stdin
		cmd := e.Command(ctx, "load")
		cmd.Stdin = image

		output, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			err = errors.Join(err, fmt.Errorf("image load failed: %s: %w", string(output), cmdErr))
		}
	}

	return err
}

// InspectVolume returns details for the specified volume names.
func (e *dockerEngine) InspectVolume(ctx context.Context, volumeNames ...string) (map[string]Volume, error) {
	if len(volumeNames) == 0 {
		return map[string]Volume{}, nil
	}

	// Ignore the error. This is because one or more of the provided names could be missing.
	// This allows for Info to report that the volume itself is missing.
	output, _ := e.CommandOutput(ctx, "system", "df", "-v", "--format={{json  .}}")

	// Anonymous struct to just pick out what we need
	volumeInfos := struct {
		Volumes []struct {
			Name       string `json:"Name"`
			Size       string `json:"Size"`
			Mountpoint string `json:"Mountpoint"`
		} `json:"Volumes"`
	}{}

	err := json.Unmarshal([]byte(output.Stdout.String()), &volumeInfos)
	if err != nil {
		return nil, fmt.Errorf("failed to decode docker volume info for %v: %w", volumeNames, err)
	}

	results := make(map[string]Volume, len(volumeNames))

	for _, name := range volumeNames {
		for _, volumeInfo := range volumeInfos.Volumes {
			if name == volumeInfo.Name {
				bytes, parseErr := humanize.ParseBytes(volumeInfo.Size)
				if parseErr != nil {
					err = errors.Join(err, parseErr)
				} else {
					results[name] = Volume{
						Name:       volumeInfo.Name,
						SizeBytes:  bytes,
						Mountpoint: volumeInfo.Mountpoint,
					}
				}

				break
			}
		}
	}

	return results, err
}
