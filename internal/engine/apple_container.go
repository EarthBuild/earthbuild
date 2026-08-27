package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"al.essio.dev/pkg/shellescape"
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
			IPv4Address string `json:"ipv4Address"`
			Address     string `json:"address"`
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

	if strings.HasPrefix(trimmed, "[") {
		var slice []T

		err := json.Unmarshal([]byte(trimmed), &slice)
		if err != nil {
			return nil, err
		}

		return slice, nil
	}

	var single T

	err := json.Unmarshal([]byte(trimmed), &single)
	if err != nil {
		return nil, err
	}

	return []T{single}, nil
}

// appleEngine implements engineDriver for the Apple container CLI.
type appleEngine struct {
	*shellEngine
}

func newAppleEngine(ctx context.Context, cfg *Config) (engineDriver, error) {
	e := &appleEngine{
		shellEngine: &shellEngine{
			BinaryName:              "container",
			RunCompatibilityArgs:    make([]string, 0),
			GlobalCompatibilityArgs: make([]string, 0),
			Log:                     cfg.Log,
		},
	}

	output, err := e.CommandOutput(ctx, "system", "status", "--format", "json")
	if err != nil {
		return nil, err
	}

	trimmedStdOut := strings.TrimSpace(output.Stdout.String())
	if trimmedStdOut == "" {
		return nil, errors.New("empty output from system status")
	}

	e.Addrs, err = resolveAddrs(e, cfg)
	if err != nil {
		return nil, fmt.Errorf("calculate buildkit URLs: %w", err)
	}

	return e, nil
}

// Metadata returns engine metadata.
func (e *appleEngine) Metadata() Metadata {
	return Metadata{
		Name:      "Apple Container",
		Scheme:    SchemeApple,
		Binary:    e.BinaryName,
		Transport: TransportShell,
		Addrs:     e.Addrs,
	}
}

// IsAvailable reports whether the Apple container CLI is functional.
func (e *appleEngine) IsAvailable(ctx context.Context) bool {
	return e.Command(ctx, "list").Run() == nil
}

// Version returns version and platform information.
func (e *appleEngine) Version(ctx context.Context) (Version, error) {
	output, err := e.CommandOutput(ctx, "--version")
	if err != nil {
		return Version{}, err
	}

	ver := strings.TrimSpace(output.Stdout.String())

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

// ListContainers returns a list of all containers.
func (e *appleEngine) ListContainers(ctx context.Context) ([]Container, error) {
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
			addr := cmp.Or(v.Status.Networks[0].IPv4Address, v.Status.Networks[0].Address)
			ip, _, _ := strings.Cut(addr, "/")
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

// InspectContainers returns information for the given container names or IDs.
func (e *appleEngine) InspectContainers(
	ctx context.Context, namesOrIDs ...string,
) ([]Container, error) {
	args := append([]string{"inspect"}, namesOrIDs...) //nolint:goconst

	// Ignore the error because non-existent containers cause the command to exit with an error.
	// Empty stdout will result in unmarshalSingleOrSlice returning nil, preserving StatusMissing.
	output, _ := e.CommandOutput(ctx, args...)

	stdout := strings.TrimSpace(output.Stdout.String())
	if stdout == "" || stdout == "[]" {
		return nil, nil
	}

	inspects, err := unmarshalSingleOrSlice[appleContainerInspect](stdout)
	if err != nil {
		return nil, fmt.Errorf("decode apple container inspect output (%s): %w", stdout, err)
	}

	containers := make([]Container, 0, len(inspects))
	for _, v := range inspects {
		ipAddresses := map[string]string{}

		if len(v.Status.Networks) > 0 {
			addr := cmp.Or(v.Status.Networks[0].IPv4Address, v.Status.Networks[0].Address)
			ip, _, _ := strings.Cut(addr, "/")
			ipAddresses["bridge"] = ip
		}

		containers = append(containers, Container{
			ID:     v.Configuration.ID,
			Name:   v.Configuration.ID,
			Status: v.Status.State,
			Image:  v.Configuration.Image.Reference,
			IPs:    ipAddresses,
			Labels: v.Configuration.Labels,
		})
	}

	return containers, nil
}

// RemoveContainer deletes the specified containers.
func (e *appleEngine) RemoveContainer(ctx context.Context, force bool, namesOrIDs ...string) error {
	args := []string{"delete"}
	if force {
		args = append(args, "-f")
	}

	args = append(args, namesOrIDs...)
	_, err := e.CommandOutput(ctx, args...)

	return err
}

// RunContainer creates and starts the specified containers.
func (e *appleEngine) RunContainer(ctx context.Context, specs ...ContainerSpec) error {
	var err error

	for _, spec := range specs {
		args := make([]string, 0, 32)
		args = append(args, "run", "--rosetta")

		if spec.Privileged {
			args = append(args, "--cap-add", "ALL", "--read-only-path", "NONE", "--masked-path", "NONE")
		}

		hasCPUs := false
		hasMemory := false

		for _, arg := range spec.AdditionalArgs {
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

		for k, v := range spec.Envs {
			env := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "--env", env)
		}

		for k, v := range spec.Labels {
			label := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "--label", label)
		}

		args = append(args, buildAppleMountArgs(spec.Mounts)...)

		for _, port := range spec.Ports {
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
		args = append(args, "--name", spec.NameOrID)
		args = append(args, spec.AdditionalArgs...)
		args = append(args, e.RunCompatibilityArgs...)
		args = append(args, spec.ImageRef)
		args = append(args, spec.ContainerArgs...)

		_, cmdErr := e.CommandOutput(ctx, args...)
		if cmdErr != nil {
			err = errors.Join(err, fmt.Errorf("run container %s: %w", spec.NameOrID, cmdErr))
		}
	}

	return err
}

// InspectImages returns metadata for the given image references.
func (e *appleEngine) InspectImages(ctx context.Context, refs ...string) ([]Image, error) {
	args := append([]string{"image", "inspect"}, refs...) //nolint:goconst

	// Ignore the error because non-existent images cause the command to exit with an error.
	// Empty stdout will result in unmarshalSingleOrSlice returning nil.
	output, _ := e.CommandOutput(ctx, args...)

	stdout := strings.TrimSpace(output.Stdout.String())
	if stdout == "" || stdout == "[]" {
		return nil, nil
	}

	inspects, err := unmarshalSingleOrSlice[appleImageInspect](stdout)
	if err != nil {
		return nil, fmt.Errorf("decode apple image inspect output (%s): %w", stdout, err)
	}

	images := make([]Image, 0, len(inspects))
	for _, v := range inspects {
		info := Image{
			ID:   v.ID,
			Tags: []string{v.Configuration.Name},
		}

		if len(v.Variants) > 0 {
			info.OS = v.Variants[0].Platform.OS
			info.Architecture = v.Variants[0].Platform.Architecture
		}

		images = append(images, info)
	}

	return images, nil
}

// PullImage downloads the specified container images.
func (e *appleEngine) PullImage(ctx context.Context, refs ...string) error {
	var err error

	for _, ref := range refs {
		args := []string{"image", "pull"}

		isLocalReg := e.Addrs.LocalRegistry != nil &&
			e.Addrs.LocalRegistry.Host != "" &&
			strings.HasPrefix(ref, e.Addrs.LocalRegistry.Host+"/")

		if strings.HasPrefix(ref, "127.0.0.1:") || strings.HasPrefix(ref, "localhost:") || isLocalReg {
			args = append(args, "--scheme", "http")
		} else if hostPart, _, ok := strings.Cut(ref, "/"); ok {
			host, _, _ := net.SplitHostPort(hostPart)
			if host == "" {
				host = hostPart
			}

			if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
				args = append(args, "--scheme", "http")
			}
		}

		args = append(args, ref)

		_, cmdErr := e.CommandOutput(ctx, args...)
		if cmdErr != nil {
			err = errors.Join(err, fmt.Errorf("pull image %s: %w", ref, cmdErr))
		}
	}

	return err
}

// TagImage applies tags to existing images.
func (e *appleEngine) TagImage(ctx context.Context, tags ...Tag) error {
	var err error

	for _, tag := range tags {
		_, cmdErr := e.CommandOutput(ctx, "image", "tag", tag.SourceRef, tag.TargetRef)
		if cmdErr != nil {
			err = errors.Join(err, fmt.Errorf("tag image %s -> %s: %w", tag.SourceRef, tag.TargetRef, cmdErr))
		}
	}

	return err
}

// RemoveImage removes images via the CLI.
func (e *appleEngine) RemoveImage(ctx context.Context, force bool, refs ...string) error {
	args := []string{"image", "rm"}
	if force {
		args = append(args, "--force")
	}

	args = append(args, refs...)
	_, err := e.CommandOutput(ctx, args...)

	return err
}

// ImageLoadCommand returns the shell command to load an image from a file.
func (e *appleEngine) ImageLoadCommand(filename string) string {
	return strings.Join(e.CommandArgs("image", "load", "--input", shellescape.Quote(filename)), " ")
}

// LoadImage reads image tarballs and loads them into the container store.
func (e *appleEngine) LoadImage(ctx context.Context, images ...io.Reader) error {
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
			err = errors.Join(err, fmt.Errorf("load image: %w", loadErr))
		}
	}

	return err
}

// InspectVolumes returns details for the specified volume names.
func (e *appleEngine) InspectVolumes(ctx context.Context, volumeNames ...string) ([]Volume, error) {
	args := append([]string{"volume", "inspect"}, volumeNames...)

	// Ignore the error because non-existent volumes cause the command to exit with an error.
	// Empty stdout will result in unmarshalSingleOrSlice returning nil.
	output, _ := e.CommandOutput(ctx, args...)

	stdout := strings.TrimSpace(output.Stdout.String())
	if stdout == "" || stdout == "[]" {
		return nil, nil
	}

	inspects, err := unmarshalSingleOrSlice[appleVolumeInspect](stdout)
	if err != nil {
		return nil, fmt.Errorf("decode apple volume inspect output for %v: %w", volumeNames, err)
	}

	volumes := make([]Volume, 0, len(inspects))
	for _, vol := range inspects {
		name := vol.Configuration.Name
		if name == "" {
			name = vol.ID
		}

		volumes = append(volumes, Volume{
			Name:       name,
			SizeBytes:  vol.Configuration.SizeInBytes,
			Mountpoint: vol.Configuration.Source,
		})
	}

	return volumes, nil
}

// buildAppleMountArgs constructs CLI mount flags for Apple Container.
func buildAppleMountArgs(mounts []Mount) []string {
	args := make([]string, 0, len(mounts)*2)

	for _, mnt := range mounts {
		mountSpec := fmt.Sprintf("type=%s,source=%s,target=%s", mnt.Type, mnt.Source, mnt.Dest)
		if mnt.ReadOnly {
			mountSpec += ",readonly"
		}

		args = append(args, "--mount", mountSpec)
	}

	return args
}

// DefaultAddr returns the default address for the Apple Container engine.
// The actual reachable bridge IP address is determined dynamically later
// via [appleEngine.ContainerAddr] once the container is running.
func (e *appleEngine) DefaultAddr(cfg *Config) (string, error) {
	return AppleSchemePrefix + cfg.LocalContainerName, nil
}

// ContainerAddr returns the reachable address for the specified port on an Apple Container.
func (e *appleEngine) ContainerAddr(ctx context.Context, containerName string, port int) (string, error) {
	containers, err := e.InspectContainers(ctx, containerName)
	if err != nil {
		return "", err
	}

	if len(containers) == 0 {
		return "", fmt.Errorf("container %s not found", containerName)
	}

	bridgeIP, ok := containers[0].IPs["bridge"]
	if !ok || bridgeIP == "" {
		return "", fmt.Errorf("container %s has no bridge IP", containerName)
	}

	return "tcp://" + net.JoinHostPort(bridgeIP, strconv.Itoa(port)), nil
}
