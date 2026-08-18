package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type appleContainerInspect struct {
	Configuration struct {
		Labels map[string]string `json:"labels"`
		ID     string            `json:"id"`
		Image  struct {
			Reference string `json:"reference"`
		} `json:"image"`
	} `json:"configuration"`
	Status struct {
		State    string `json:"state"`
		Networks []struct {
			Address string `json:"address"`
		} `json:"networks"`
	} `json:"status"`
}

type appleImageInspect struct {
	Configuration struct {
		Name       string `json:"name"`
		Descriptor struct {
			Digest string `json:"digest"`
		} `json:"descriptor"`
	} `json:"configuration"`
	ID       string `json:"id"`
	Variants []struct {
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"variants"`
}

type appleVolumeInspect struct {
	ID            string `json:"id"`
	Configuration struct {
		Name        string `json:"name"`
		Source      string `json:"source"`
		SizeInBytes uint64 `json:"sizeInBytes"`
	} `json:"configuration"`
}

func unmarshalSingleOrSlice[T any](data string) ([]T, error) {
	trimmed := strings.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}

	if trimmed[0] == '[' {
		var items []T

		err := json.Unmarshal([]byte(trimmed), &items)
		if err != nil {
			return nil, err
		}

		return items, nil
	}

	var item T

	err := json.Unmarshal([]byte(trimmed), &item)
	if err != nil {
		return nil, err
	}

	return []T{item}, nil
}

// appleEngine implements Engine for the Apple Container CLI.
type appleEngine struct {
	*shellEngine
}

// newAppleEngine constructs a new Engine using the apple container binary.
func newAppleEngine(_ context.Context, cfg *Config) (engineDriver, error) {
	e := &appleEngine{
		shellEngine: &shellEngine{
			BinaryName:              "container",
			RunCompatibilityArgs:    make([]string, 0),
			GlobalCompatibilityArgs: make([]string, 0),
			Console:                 cfg.Console,
		},
	}

	var err error

	e.Endpoints, err = e.ResolveEndpoints(DriverAppleContainerShell, cfg)
	if err != nil {
		return nil, fmt.Errorf("calculate buildkit URLs: %w", err)
	}

	return e, nil
}

// Metadata returns current engine metadata.
func (e *appleEngine) Metadata() Metadata {
	return Metadata{
		Name:      "Apple Container",
		Scheme:    SchemeAppleContainer,
		Binary:    e.BinaryName,
		Transport: TransportShell,
		Endpoints: e.Endpoints,
	}
}

// IsAvailable reports whether the container command is installed and functioning.
func (e *appleEngine) IsAvailable(ctx context.Context) bool {
	return e.Command(ctx, "list").Run() == nil
}

// Version returns version and platform information for the container CLI.
func (e *appleEngine) Version(ctx context.Context) (Version, error) {
	output, err := e.CommandOutput(ctx, "--version")
	if err != nil {
		return Version{}, err
	}

	ver := strings.TrimSpace(output.String())

	return Version{
		ClientVersion:    ver,
		ClientAPIVersion: "N/A",
		ClientPlatform:   "darwin/arm64",
		ServerVersion:    ver,
		ServerAPIVersion: "N/A",
		ServerPlatform:   "darwin/arm64",
		ServerAddress:    "local",
	}, nil
}

// ContainerList returns a list of all containers.
func (e *appleEngine) ContainerList(ctx context.Context) ([]Container, error) {
	output, err := e.CommandOutput(ctx, "list", "--format", "json", "--all")
	if err != nil {
		return nil, err
	}

	var inspects []appleContainerInspect

	err = json.Unmarshal([]byte(output.Stdout.String()), &inspects)
	if err != nil {
		return nil, fmt.Errorf("decode apple container list output (%s): %w", output.Stdout.String(), err)
	}

	ret := make([]Container, len(inspects))
	for i, v := range inspects {
		ipAddresses := map[string]string{}

		if len(v.Status.Networks) > 0 {
			ip, _, _ := strings.Cut(v.Status.Networks[0].Address, "/")
			ipAddresses["bridge"] = ip
		}

		ret[i] = Container{
			ID:     v.Configuration.ID,
			Name:   v.Configuration.ID,
			Status: v.Status.State,
			Image:  v.Configuration.Image.Reference,
			IPs:    ipAddresses,
		}
	}

	return ret, nil
}

// ContainerInfo returns information for the given container names or IDs.
func (e *appleEngine) ContainerInfo(
	ctx context.Context, namesOrIDs ...string,
) (map[string]Container, error) {
	infos := make(map[string]Container, len(namesOrIDs))
	for _, nameOrID := range namesOrIDs {
		infos[nameOrID] = Container{
			Name:   nameOrID,
			Status: StatusMissing,
		}
	}

	if len(namesOrIDs) == 0 {
		return infos, nil
	}

	args := append([]string{"inspect"}, namesOrIDs...) //nolint:goconst

	// Ignore the error because non-existent containers cause the command to exit with an error.
	// Empty stdout will result in unmarshalSingleOrSlice returning nil, preserving StatusMissing.
	output, _ := e.CommandOutput(ctx, args...)

	inspects, err := unmarshalSingleOrSlice[appleContainerInspect](output.Stdout.String())
	if err != nil {
		return nil, fmt.Errorf("decode apple container inspect output (%s): %w", output.Stdout.String(), err)
	}

	for i, v := range inspects {
		ipAddresses := map[string]string{}

		if len(v.Status.Networks) > 0 {
			ip, _, _ := strings.Cut(v.Status.Networks[0].Address, "/")
			ipAddresses["bridge"] = ip
		}

		infos[namesOrIDs[i]] = Container{
			ID:     v.Configuration.ID,
			Name:   v.Configuration.ID,
			Status: v.Status.State,
			Image:  v.Configuration.Image.Reference,
			IPs:    ipAddresses,
			Labels: v.Configuration.Labels,
		}
	}

	return infos, nil
}

// ContainerRemove deletes the specified containers.
func (e *appleEngine) ContainerRemove(ctx context.Context, force bool, namesOrIDs ...string) error {
	args := []string{"delete"}
	if force {
		args = append(args, "-f")
	}

	args = append(args, namesOrIDs...)
	_, err := e.CommandOutput(ctx, args...)

	return err
}

// ContainerRun creates and starts the specified containers.
func (e *appleEngine) ContainerRun(ctx context.Context, containers ...RunConfig) error {
	var err error

	for _, cfg := range containers {
		args := make([]string, 0, 32)
		args = append(args, "run", "--rosetta")

		if cfg.Privileged {
			args = append(args, "--cap-add", "ALL", "--read-only-path", "NONE", "--masked-path", "NONE")
		}

		hasCPUs := false
		hasMemory := false

		for _, arg := range cfg.AdditionalArgs {
			if arg == "-c" || arg == "--cpus" || strings.HasPrefix(arg, "--cpus=") {
				hasCPUs = true
			}

			if arg == "-m" || arg == "--memory" || strings.HasPrefix(arg, "--memory=") {
				hasMemory = true
			}
		}

		cpus, memMB := defaultContainerResources()
		if !hasCPUs && cpus > 0 {
			args = append(args, "-c", strconv.Itoa(cpus))
		}

		if !hasMemory && memMB > 0 {
			args = append(args, "-m", fmt.Sprintf("%dM", memMB))
		}

		for k, v := range cfg.Envs {
			env := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "--env", env)
		}

		for k, v := range cfg.Labels {
			label := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "--label", label)
		}

		args = append(args, buildAppleMountArgs(cfg.Mounts)...)

		for _, port := range cfg.Ports {
			hostPort := strconv.Itoa(port.HostPort)
			if port.HostPort <= 0 {
				hostPort = ""
			}

			portStr := fmt.Sprintf("%s:%s:%d", port.IP, hostPort, port.ContainerPort)

			if port.Protocol != "" {
				portStr = fmt.Sprintf("%s/%s", portStr, port.Protocol)
			}

			args = append(args, "--publish", portStr)
		}

		args = append(args, "-d")
		args = append(args, "--name", cfg.NameOrID)
		args = append(args, cfg.AdditionalArgs...)
		args = append(args, e.RunCompatibilityArgs...)
		args = append(args, cfg.ImageRef)
		args = append(args, cfg.ContainerArgs...)

		_, cmdErr := e.CommandOutput(ctx, args...)
		if cmdErr != nil {
			err = errors.Join(err, cmdErr)
		}
	}

	return err
}

// ImageInfo returns metadata for the given image references.
func (e *appleEngine) ImageInfo(ctx context.Context, refs ...string) (map[string]Image, error) {
	infos := make(map[string]Image, len(refs))

	if len(refs) == 0 {
		return infos, nil
	}

	args := append([]string{"image", "inspect"}, refs...) //nolint:goconst

	// Ignore the error because non-existent images cause the command to exit with an error.
	// Empty stdout will result in unmarshalSingleOrSlice returning nil.
	output, _ := e.CommandOutput(ctx, args...)

	inspects, err := unmarshalSingleOrSlice[appleImageInspect](output.Stdout.String())
	if err != nil {
		return nil, fmt.Errorf("decode apple image inspect output (%s): %w", output.Stdout.String(), err)
	}

	for idx, v := range inspects {
		info := Image{
			ID:   v.ID,
			Tags: []string{v.Configuration.Name},
		}

		if len(v.Variants) > 0 {
			info.OS = v.Variants[0].Platform.OS
			info.Architecture = v.Variants[0].Platform.Architecture
		}

		infos[refs[idx]] = info
	}

	return infos, nil
}

// ImagePull downloads the specified container images.
func (e *appleEngine) ImagePull(ctx context.Context, refs ...string) error {
	var err error

	for _, ref := range refs {
		args := []string{"image", "pull"}
		if strings.HasPrefix(ref, e.Endpoints.LocalRegistryHost.Host+"/") {
			args = append(args, "--scheme", "http")
		}

		args = append(args, ref)

		_, cmdErr := e.CommandOutput(ctx, args...)
		if cmdErr != nil {
			err = errors.Join(err, cmdErr)
		}
	}

	return err
}

// ImageTag applies tags to existing images.
func (e *appleEngine) ImageTag(ctx context.Context, tags ...Tag) error {
	var err error

	for _, tag := range tags {
		_, cmdErr := e.CommandOutput(ctx, "image", "tag", tag.SourceRef, tag.TargetRef)
		if cmdErr != nil {
			err = errors.Join(err, cmdErr)
		}
	}

	return err
}

// ImageLoadCommand returns the shell command to load an image from a file.
func (e *appleEngine) ImageLoadCommand(filename string) string {
	return strings.Join(e.CommandArgs("image", "load", "--input", filename), " ")
}

// ImageLoad reads image tarballs and loads them into the container store.
func (e *appleEngine) ImageLoad(ctx context.Context, images ...io.Reader) error {
	var err error

	for _, image := range images {
		loadErr := func() error {
			file, tmpErr := os.CreateTemp("", "earth-apple-load-*")
			if tmpErr != nil {
				return fmt.Errorf("create temp tarball: %w", tmpErr)
			}
			defer os.Remove(file.Name())

			_, copyErr := io.Copy(file, image)
			if copyErr != nil {
				_ = file.Close()
				return fmt.Errorf("write to %s: %w", file.Name(), copyErr)
			}

			closeErr := file.Close()
			if closeErr != nil {
				return fmt.Errorf("close %s: %w", file.Name(), closeErr)
			}

			output, cmdErr := e.CommandOutput(ctx, "image", "load", "--input", file.Name())
			if cmdErr != nil {
				return fmt.Errorf("load image (%s): %w", output.String(), cmdErr)
			}

			return nil
		}()
		if loadErr != nil {
			err = errors.Join(err, loadErr)
		}
	}

	return err
}

// VolumeInfo returns details for the specified volume names.
func (e *appleEngine) VolumeInfo(ctx context.Context, volumeNames ...string) (map[string]Volume, error) {
	if len(volumeNames) == 0 {
		return map[string]Volume{}, nil
	}

	args := append([]string{"volume", "inspect"}, volumeNames...)

	// Ignore the error because non-existent volumes cause the command to exit with an error.
	// Empty stdout will result in unmarshalSingleOrSlice returning nil.
	output, _ := e.CommandOutput(ctx, args...)

	inspects, err := unmarshalSingleOrSlice[appleVolumeInspect](output.Stdout.String())
	if err != nil {
		return nil, fmt.Errorf("decode apple volume inspect output for %v: %w", volumeNames, err)
	}

	results := make(map[string]Volume, len(inspects))

	for _, vol := range inspects {
		name := vol.Configuration.Name
		if name == "" {
			name = vol.ID
		}

		vi := Volume{
			Name:       name,
			SizeBytes:  vol.Configuration.SizeInBytes,
			Mountpoint: vol.Configuration.Source,
		}
		if slices.Contains(volumeNames, name) {
			results[name] = vi
		}

		if vol.ID != "" && slices.Contains(volumeNames, vol.ID) {
			results[vol.ID] = vi
		}
	}

	return results, nil
}

// appleBindFileDir converts a single-file bind mount into a directory mount,
// as Apple Container requires directory-level bind mounts.
func appleBindFileDir(mnt Mount, seenDirs map[string]struct{}) (string, bool) {
	if mnt.Type != MountBind {
		return "", false
	}

	fileInfo, err := os.Stat(mnt.Source)
	if err != nil || fileInfo.IsDir() {
		return "", false
	}

	dir := filepath.Dir(mnt.Source)
	if _, seen := seenDirs[dir]; seen {
		return "", true
	}

	seenDirs[dir] = struct{}{}
	mountSpec := fmt.Sprintf("type=bind,source=%s,target=/etc/earthly-certs", dir)

	if mnt.ReadOnly {
		mountSpec += ",readonly" //nolint:goconst
	}

	return mountSpec, true
}

// buildAppleMountArgs constructs CLI mount flags for Apple Container.
func buildAppleMountArgs(mounts []Mount) []string {
	var args []string

	seenMountDirs := make(map[string]struct{}, len(mounts))

	for _, mnt := range mounts {
		mountSpec, handled := appleBindFileDir(mnt, seenMountDirs)
		if handled {
			if mountSpec != "" {
				args = append(args, "--mount", mountSpec)
			}

			continue
		}

		mountSpec = fmt.Sprintf("type=%s,source=%s,target=%s", mnt.Type, mnt.Source, mnt.Dest)

		if mnt.ReadOnly {
			mountSpec += ",readonly"
		}

		args = append(args, "--mount", mountSpec)
	}

	return args
}
