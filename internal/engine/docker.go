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
			Log:                     cfg.Log,
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

	e.Addrs, err = resolveAddrs(e, cfg)
	if err != nil {
		return nil, fmt.Errorf("calculate buildkit URLs: %w", err)
	}

	output, err = e.CommandOutput(ctx, "info", "--format={{.DockerRootDir}}")
	if err != nil {
		// Maybe the user has aliased podman=docker?
		var err2 error

		output, err2 = e.CommandOutput(ctx, "info", "--format={{.Store.GraphRoot}}")
		if err2 != nil {
			return nil, fmt.Errorf("get docker root dir: %w", err)
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
		Addrs:     e.Addrs,
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
		return Version{}, fmt.Errorf("parse docker version output: %w", err)
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

// InspectVolumes returns details for the specified volume names.
func (e *dockerEngine) InspectVolumes(ctx context.Context, volumeNames ...string) ([]Volume, error) {
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
		return nil, fmt.Errorf("decode docker volume info for %v: %w", volumeNames, err)
	}

	volumes := make([]Volume, 0, len(volumeInfos.Volumes))
	for _, volumeInfo := range volumeInfos.Volumes {
		bytes, parseErr := humanize.ParseBytes(volumeInfo.Size)
		if parseErr != nil {
			err = errors.Join(err, fmt.Errorf("parse volume size %q for %s: %w", volumeInfo.Size, volumeInfo.Name, parseErr))
			continue
		}

		volumes = append(volumes, Volume{
			Name:       volumeInfo.Name,
			SizeBytes:  bytes,
			Mountpoint: volumeInfo.Mountpoint,
		})
	}

	return volumes, err
}

// DefaultAddr returns the default address for the Docker engine.
func (e *dockerEngine) DefaultAddr(cfg *Config) (string, error) {
	return DockerSchemePrefix + cfg.LocalContainerName, nil
}

// ContainerAddr returns the reachable address for the specified port on a Docker container.
func (e *dockerEngine) ContainerAddr(_ context.Context, containerName string, _ int) (string, error) {
	return DockerSchemePrefix + containerName, nil
}
