package containerutil

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/dustin/go-humanize"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // Load "docker-container://" helper.
)

type dockerShellFrontend struct {
	*shellFrontend

	userNamespaced bool
}

// NewDockerShellFrontend constructs a new Frontend using the docker binary installed on the host.
// It also ensures that the binary is functional for our needs and collects compatibility information.
func NewDockerShellFrontend(ctx context.Context, cfg *FrontendConfig) (ContainerFrontend, error) {
	fe := &dockerShellFrontend{
		shellFrontend: &shellFrontend{
			binaryName:              FrontendDocker,
			runCompatibilityArgs:    make([]string, 0),
			globalCompatibilityArgs: make([]string, 0),
			Log:                     cfg.Log,
		},
	}

	security, rootDir, err := fe.probe(ctx)
	if err != nil {
		return nil, err
	}

	fe.rootless = strings.Contains(security, "rootless")

	fe.userNamespaced = strings.Contains(security, "name=userns")
	if fe.userNamespaced {
		fe.runCompatibilityArgs = []string{"--userns", "host"}
	}

	fe.urls, err = fe.setupAndValidateAddresses(FrontendDockerShell, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate buildkit URLs: %w", err)
	}

	outputStr := strings.TrimSpace(rootDir)
	if outputStr == "/var/lib/containers/storage" {
		// Likely podman making itself available via the docker CLI.
		// This can happen either when podman set /var/run/docker.sock itself,
		// or when the user has aliased podman=docker.
		fe.likelyPodman = true
	}

	return fe, nil
}

func (dsf *dockerShellFrontend) Scheme() string {
	return SchemeDockerContainer
}

func (dsf *dockerShellFrontend) Config() *CurrentFrontend {
	return &CurrentFrontend{
		Setting:      FrontendDockerShell,
		Binary:       dsf.binaryName,
		Type:         FrontendTypeShell,
		FrontendURLs: dsf.urls,
	}
}

func (dsf *dockerShellFrontend) Information(ctx context.Context) (*FrontendInfo, error) {
	output, err := dsf.commandContextOutput(ctx, "version", "--format={{json .}}")
	if err != nil {
		return nil, err
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

	err = json.Unmarshal([]byte(output.string()), &allInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to parse docker version output: %w", err)
	}

	host, exists := os.LookupEnv("DOCKER_HOST")
	if !exists {
		host = "/var/run/docker.sock"
	}

	return &FrontendInfo{
		ClientVersion:    allInfo.Client.Version,
		ClientAPIVersion: allInfo.Client.APIVersion,
		ClientPlatform:   fmt.Sprintf("%s/%s", allInfo.Client.OS, allInfo.Client.Arch),
		ServerVersion:    allInfo.Server.Version,
		ServerAPIVersion: allInfo.Server.APIVersion,
		ServerPlatform:   fmt.Sprintf("%s/%s", allInfo.Server.OS, allInfo.Server.Arch),
		ServerAddress:    host,
	}, nil
}

func (dsf *dockerShellFrontend) ContainerInfo(
	ctx context.Context, namesOrIDs ...string,
) (map[string]*ContainerInfo, error) {
	results, err := dsf.shellFrontend.ContainerInfo(ctx, namesOrIDs...)
	if err != nil {
		return nil, err
	}

	for _, v := range results {
		// Docker prepends a `\`. This is as intended, according to docker; but unexpected in our
		// case. So remove it. If the status is missing, it was passed through so do not remove.
		if v.Status != StatusMissing {
			v.Name = v.Name[1:]
		}
	}

	return results, nil
}

func (dsf *dockerShellFrontend) ImagePull(ctx context.Context, refs ...string) error {
	var err error

	for _, ref := range refs {
		_, cmdErr := dsf.commandContextOutput(ctx, "pull", ref)
		if cmdErr != nil {
			err = errors.Join(err, cmdErr)
		}
	}

	return err
}

func (dsf *dockerShellFrontend) ImageLoadFromFileCommand(filename string) string {
	binary, args := dsf.commandContextStrings("load")

	all := append([]string{binary}, args...)

	return fmt.Sprintf("cat %s | %s", shellescape.Quote(filename), strings.Join(all, " "))
}

func (dsf *dockerShellFrontend) ImageLoad(ctx context.Context, images ...io.Reader) error {
	var err error

	args := append(dsf.globalCompatibilityArgs, "load") //nolint:gocritic
	for _, image := range images {
		// Do not use the wrapper to allow the image to come in on stdin
		cmd := exec.CommandContext(ctx, dsf.binaryName, args...) // #nosec G204
		cmd.Stdin = image

		output, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			err = errors.Join(err, fmt.Errorf("image load failed: %s: %w", string(output), cmdErr))
		}
	}

	return err
}

func (dsf *dockerShellFrontend) VolumeInfo(ctx context.Context, volumeNames ...string) (map[string]*VolumeInfo, error) {
	// Ignore the error. This is because one or more of the provided names could be missing.
	// This allows for Info to report that the volume itself is missing.
	output, _ := dsf.commandContextOutput(ctx, "system", "df", "-v", "--format={{json  .}}")

	results := map[string]*VolumeInfo{}
	for _, name := range volumeNames {
		// Preinitialize all as missing. It will get overwritten when we encounter a real one from the actual output.
		results[name] = &VolumeInfo{Name: name}
	}

	// Anonymous struct to just pick out what we need
	volumeInfos := struct {
		Volumes []struct {
			Name       string `json:"Name"`
			Size       string `json:"Size"`
			Mountpoint string `json:"Mountpoint"`
		} `json:"Volumes"`
	}{}

	err := json.Unmarshal([]byte(output.stdout.String()), &volumeInfos)
	if err != nil {
		return nil, fmt.Errorf("failed to decode docker volume info for %v: %w", volumeNames, err)
	}

	for _, name := range volumeNames {
		for _, volumeInfo := range volumeInfos.Volumes {
			if name == volumeInfo.Name {
				bytes, parseErr := humanize.ParseBytes(volumeInfo.Size)
				if parseErr != nil {
					err = errors.Join(err, parseErr)
				} else {
					results[name] = &VolumeInfo{
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

// probeSeparator divides the two answers asked for in one question. Neither a
// security option nor a path contains it, and both contain spaces and commas,
// which is why it is not one of those.
const probeSeparator = "|"

// probe asks the daemon what this frontend needs to know, in one question.
//
// `docker info` talks to the daemon and costs about a tenth of a second each
// time. Three of them ran here before any command was dispatched, so every
// invocation - including the many that never touch Docker - paid for answers it
// usually did not use.
//
// **The three-call form is still here, and still says what it always said.** It
// was not only slow: the bare `info` came first because `docker info --format`
// panics when the daemon is down, and printing a panic from somebody else's
// binary is not a diagnosis. So the one question is *tried*, and anything other
// than an answer - a panic, a daemon that is not there, a field this daemon does
// not have - falls through to the sequence that knows how to tell those apart.
func (dsf *dockerShellFrontend) probe(ctx context.Context) (security, rootDir string, err error) {
	one, err := dsf.commandContextOutput(ctx, "info",
		"--format={{.SecurityOptions}}"+probeSeparator+"{{.DockerRootDir}}")
	if err == nil {
		both := strings.SplitN(strings.TrimSpace(one.string()), probeSeparator, 2)
		if len(both) == 2 && both[1] != "" {
			return both[0], both[1], nil
		}
	}

	// Whether docker is there at all, asked without a template so that a
	// stopped daemon is reported rather than panicked over.
	_, err = dsf.commandContextOutput(ctx, "info")
	if err != nil {
		return "", "", err
	}

	output, err := dsf.commandContextOutput(ctx, "info", "--format={{.SecurityOptions}}")
	if err != nil {
		return "", "", err
	}

	security = output.string()

	output, err = dsf.commandContextOutput(ctx, "info", "--format={{.DockerRootDir}}")
	if err != nil {
		// Maybe the user has aliased podman=docker?
		// (The same information is found at a different path in podman)
		var err2 error

		output, err2 = dsf.commandContextOutput(ctx, "info", "--format={{.Store.GraphRoot}}")
		if err2 != nil {
			return "", "", fmt.Errorf("failed to get docker root dir: %w", err)
		}
	}

	return security, output.string(), nil
}
