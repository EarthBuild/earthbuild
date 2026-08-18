package containerutil

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

type appleShellFrontend struct {
	*shellFrontend
}

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

// NewAppleContainerShellFrontend constructs a new Frontend using the apple container binary.
func NewAppleContainerShellFrontend(_ context.Context, cfg *FrontendConfig) (ContainerFrontend, error) {
	fe := &appleShellFrontend{
		shellFrontend: &shellFrontend{
			binaryName:              "container",
			runCompatibilityArgs:    make([]string, 0),
			globalCompatibilityArgs: make([]string, 0),
			Console:                 cfg.Console,
		},
	}

	var err error

	fe.urls, err = fe.setupAndValidateAddresses(FrontendAppleContainerShell, cfg)
	if err != nil {
		return nil, fmt.Errorf("calculate buildkit URLs: %w", err)
	}

	return fe, nil
}

// Scheme returns the scheme used for apple-container addresses.
func (asf *appleShellFrontend) Scheme() string {
	return SchemeAppleContainer
}

// Config returns current frontend configuration settings.
func (asf *appleShellFrontend) Config() *CurrentFrontend {
	return &CurrentFrontend{
		Setting:      FrontendAppleContainerShell,
		Binary:       asf.binaryName,
		Type:         FrontendTypeShell,
		FrontendURLs: asf.urls,
	}
}

// IsAvailable reports whether the container command is installed and functioning.
func (asf *appleShellFrontend) IsAvailable(ctx context.Context) bool {
	return asf.command(ctx, "list").Run() == nil
}

// Information returns version and platform information for the container CLI.
func (asf *appleShellFrontend) Information(ctx context.Context) (FrontendInfo, error) {
	output, err := asf.commandOutput(ctx, "--version")
	if err != nil {
		return FrontendInfo{}, err
	}

	ver := strings.TrimSpace(output.string())

	return FrontendInfo{
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
func (asf *appleShellFrontend) ContainerList(ctx context.Context) ([]ContainerInfo, error) {
	output, err := asf.commandOutput(ctx, "list", "--format", "json", "--all")
	if err != nil {
		return nil, err
	}

	var inspects []appleContainerInspect

	err = json.Unmarshal([]byte(output.stdout.String()), &inspects)
	if err != nil {
		return nil, fmt.Errorf("decode apple container list output (%s): %w", output.stdout.String(), err)
	}

	ret := make([]ContainerInfo, len(inspects))
	for i, v := range inspects {
		ipAddresses := map[string]string{}

		if len(v.Status.Networks) > 0 {
			ip, _, _ := strings.Cut(v.Status.Networks[0].Address, "/")
			ipAddresses["bridge"] = ip
		}

		ret[i] = ContainerInfo{
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
func (asf *appleShellFrontend) ContainerInfo(
	ctx context.Context, namesOrIDs ...string,
) (map[string]ContainerInfo, error) {
	infos := make(map[string]ContainerInfo, len(namesOrIDs))
	for _, nameOrID := range namesOrIDs {
		infos[nameOrID] = ContainerInfo{
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
	output, _ := asf.commandOutput(ctx, args...)

	inspects, err := unmarshalSingleOrSlice[appleContainerInspect](output.stdout.String())
	if err != nil {
		return nil, fmt.Errorf("decode apple container inspect output (%s): %w", output.stdout.String(), err)
	}

	for i, v := range inspects {
		ipAddresses := map[string]string{}

		if len(v.Status.Networks) > 0 {
			ip, _, _ := strings.Cut(v.Status.Networks[0].Address, "/")
			ipAddresses["bridge"] = ip
		}

		infos[namesOrIDs[i]] = ContainerInfo{
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
func (asf *appleShellFrontend) ContainerRemove(ctx context.Context, force bool, namesOrIDs ...string) error {
	args := []string{"delete"}
	if force {
		args = append(args, "-f")
	}

	args = append(args, namesOrIDs...)
	_, err := asf.commandOutput(ctx, args...)

	return err
}

// ContainerRun creates and starts the specified containers.
func (asf *appleShellFrontend) ContainerRun(ctx context.Context, containers ...ContainerRun) error {
	var err error

	for _, container := range containers {
		args := make([]string, 0, 32)
		args = append(args, "run", "--rosetta")

		if container.Privileged {
			args = append(args, "--cap-add", "ALL", "--read-only-path", "NONE", "--masked-path", "NONE")
		}

		hasCPUs := false
		hasMemory := false

		for _, arg := range container.AdditionalArgs {
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

		for k, v := range container.Envs {
			env := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "--env", env)
		}

		for k, v := range container.Labels {
			label := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "--label", label)
		}

		args = append(args, buildAppleMountArgs(container.Mounts)...)

		for _, prt := range container.Ports {
			hostPort := strconv.Itoa(prt.HostPort)
			if prt.HostPort <= 0 {
				hostPort = ""
			}

			port := fmt.Sprintf("%s:%s:%d", prt.IP, hostPort, prt.ContainerPort)

			if prt.Protocol != "" {
				port = fmt.Sprintf("%s/%s", port, prt.Protocol)
			}

			args = append(args, "--publish", port)
		}

		args = append(args, "-d")
		args = append(args, "--name", container.NameOrID)
		args = append(args, container.AdditionalArgs...)
		args = append(args, asf.runCompatibilityArgs...)
		args = append(args, container.ImageRef)
		args = append(args, container.ContainerArgs...)

		_, cmdErr := asf.commandOutput(ctx, args...)
		if cmdErr != nil {
			err = errors.Join(err, cmdErr)
		}
	}

	return err
}

// ImageInfo returns metadata for the given image references.
func (asf *appleShellFrontend) ImageInfo(ctx context.Context, refs ...string) (map[string]ImageInfo, error) {
	infos := make(map[string]ImageInfo, len(refs))

	if len(refs) == 0 {
		return infos, nil
	}

	args := append([]string{"image", "inspect"}, refs...) //nolint:goconst

	// Ignore the error because non-existent images cause the command to exit with an error.
	// Empty stdout will result in unmarshalSingleOrSlice returning nil.
	output, _ := asf.commandOutput(ctx, args...)

	inspects, err := unmarshalSingleOrSlice[appleImageInspect](output.stdout.String())
	if err != nil {
		return nil, fmt.Errorf("decode apple image inspect output (%s): %w", output.stdout.String(), err)
	}

	for i, v := range inspects {
		info := ImageInfo{
			ID:   v.ID,
			Tags: []string{v.Configuration.Name},
		}

		if len(v.Variants) > 0 {
			info.OS = v.Variants[0].Platform.OS
			info.Architecture = v.Variants[0].Platform.Architecture
		}

		infos[refs[i]] = info
	}

	return infos, nil
}

// ImagePull downloads the specified container images.
func (asf *appleShellFrontend) ImagePull(ctx context.Context, refs ...string) error {
	var err error

	for _, ref := range refs {
		args := []string{"image", "pull"}
		if strings.HasPrefix(ref, asf.urls.LocalRegistryHost.Host+"/") {
			args = append(args, "--scheme", "http")
		}

		args = append(args, ref)

		_, cmdErr := asf.commandOutput(ctx, args...)
		if cmdErr != nil {
			err = errors.Join(err, cmdErr)
		}
	}

	return err
}

// ImageTag applies tags to existing images.
func (asf *appleShellFrontend) ImageTag(ctx context.Context, tags ...ImageTag) error {
	var err error

	for _, tag := range tags {
		_, cmdErr := asf.commandOutput(ctx, "image", "tag", tag.SourceRef, tag.TargetRef)
		if cmdErr != nil {
			err = errors.Join(err, cmdErr)
		}
	}

	return err
}

// ImageLoadFromFileCommand returns the shell command to load an image from a file.
func (asf *appleShellFrontend) ImageLoadFromFileCommand(filename string) string {
	return strings.Join(asf.commandArgs("image", "load", "--input", filename), " ")
}

// ImageLoad reads image tarballs and loads them into the container store.
func (asf *appleShellFrontend) ImageLoad(ctx context.Context, images ...io.Reader) error {
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

			output, cmdErr := asf.commandOutput(ctx, "image", "load", "--input", file.Name())
			if cmdErr != nil {
				return fmt.Errorf("load image (%s): %w", output.string(), cmdErr)
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
func (asf *appleShellFrontend) VolumeInfo(ctx context.Context, volumeNames ...string) (map[string]VolumeInfo, error) {
	if len(volumeNames) == 0 {
		return map[string]VolumeInfo{}, nil
	}

	args := append([]string{"volume", "inspect"}, volumeNames...)

	// Ignore the error because non-existent volumes cause the command to exit with an error.
	// Empty stdout will result in unmarshalSingleOrSlice returning nil.
	output, _ := asf.commandOutput(ctx, args...)

	inspects, err := unmarshalSingleOrSlice[appleVolumeInspect](output.stdout.String())
	if err != nil {
		return nil, fmt.Errorf("decode apple volume inspect output for %v: %w", volumeNames, err)
	}

	results := make(map[string]VolumeInfo, len(inspects))

	for _, v := range inspects {
		name := v.Configuration.Name
		if name == "" {
			name = v.ID
		}

		vi := VolumeInfo{
			Name:       name,
			SizeBytes:  v.Configuration.SizeInBytes,
			Mountpoint: v.Configuration.Source,
		}
		if slices.Contains(volumeNames, name) {
			results[name] = vi
		}

		if v.ID != "" && slices.Contains(volumeNames, v.ID) {
			results[v.ID] = vi
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
		mountSpec += ",readonly"
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
