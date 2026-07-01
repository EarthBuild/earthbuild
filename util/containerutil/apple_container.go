package containerutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
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

	fe.rootless = false

	var err error

	fe.urls, err = fe.setupAndValidateAddresses(FrontendAppleContainerShell, cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to calculate buildkit URLs")
	}

	return fe, nil
}

func (asf *appleShellFrontend) Scheme() string {
	return SchemeAppleContainer
}

func (asf *appleShellFrontend) Config() *CurrentFrontend {
	return &CurrentFrontend{
		Setting:      FrontendAppleContainerShell,
		Binary:       asf.binaryName,
		Type:         FrontendTypeShell,
		FrontendURLs: asf.urls,
	}
}

func (asf *appleShellFrontend) IsAvailable(ctx context.Context) bool {
	args := append(asf.globalCompatibilityArgs, "list")      //nolint:gocritic
	cmd := exec.CommandContext(ctx, asf.binaryName, args...) // #nosec G204
	err := cmd.Run()

	return err == nil
}

func (asf *appleShellFrontend) Information(ctx context.Context) (*FrontendInfo, error) {
	output, err := asf.commandContextOutput(ctx, "--version")
	if err != nil {
		return nil, err
	}

	ver := strings.TrimSpace(output.string())

	return &FrontendInfo{
		ClientVersion:    ver,
		ClientAPIVersion: "N/A",
		ClientPlatform:   "darwin/arm64",
		ServerVersion:    ver,
		ServerAPIVersion: "N/A",
		ServerPlatform:   "darwin/arm64",
		ServerAddress:    "local",
	}, nil
}

func (asf *appleShellFrontend) ContainerList(ctx context.Context) ([]*ContainerInfo, error) {
	output, err := asf.commandContextOutput(ctx, "list", "--format", "json", "--all")
	if err != nil {
		return nil, err
	}

	var inspects []appleContainerInspect

	err = json.Unmarshal([]byte(output.stdout.String()), &inspects)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode apple container list output: %s", output.stdout.String())
	}

	ret := make([]*ContainerInfo, len(inspects))
	for i, v := range inspects {
		ipAddresses := map[string]string{}

		if len(v.Status.Networks) > 0 {
			ip := v.Status.Networks[0].Address
			if idx := strings.Index(ip, "/"); idx != -1 {
				ip = ip[:idx]
			}

			ipAddresses["bridge"] = ip
		}

		ret[i] = &ContainerInfo{
			ID:     v.Configuration.ID,
			Name:   v.Configuration.ID,
			Status: v.Status.State,
			Image:  v.Configuration.Image.Reference,
			IPs:    ipAddresses,
		}
	}

	return ret, nil
}

func (asf *appleShellFrontend) ContainerInfo(
	ctx context.Context, namesOrIDs ...string,
) (map[string]*ContainerInfo, error) {
	infos := map[string]*ContainerInfo{}
	for _, nameOrID := range namesOrIDs {
		infos[nameOrID] = &ContainerInfo{
			Name:   nameOrID,
			Status: StatusMissing,
		}
	}

	if len(namesOrIDs) == 0 {
		return infos, nil
	}

	args := append([]string{"inspect"}, namesOrIDs...) //nolint:goconst
	output, _ := asf.commandContextOutput(ctx, args...)

	if strings.TrimSpace(output.stdout.String()) == "" {
		return infos, nil
	}

	var inspects []appleContainerInspect

	err := json.Unmarshal([]byte(output.stdout.String()), &inspects)
	if err != nil {
		var singleInspect appleContainerInspect

		singleErr := json.Unmarshal([]byte(output.stdout.String()), &singleInspect)
		if singleErr == nil {
			inspects = []appleContainerInspect{singleInspect}
		} else {
			return nil, errors.Wrapf(err, "failed to decode apple container inspect output: %s", output.stdout.String())
		}
	}

	for _, v := range inspects {
		ipAddresses := map[string]string{}

		if len(v.Status.Networks) > 0 {
			ip := v.Status.Networks[0].Address
			if idx := strings.Index(ip, "/"); idx != -1 {
				ip = ip[:idx]
			}

			ipAddresses["bridge"] = ip
		}

		info := &ContainerInfo{
			ID:     v.Configuration.ID,
			Name:   v.Configuration.ID,
			Status: v.Status.State,
			Image:  v.Configuration.Image.Reference,
			IPs:    ipAddresses,
			Labels: v.Configuration.Labels,
		}

		for _, requested := range namesOrIDs {
			if requested == v.Configuration.ID {
				infos[requested] = info
			}
		}
	}

	return infos, nil
}

func (asf *appleShellFrontend) ContainerRemove(ctx context.Context, force bool, namesOrIDs ...string) error {
	args := []string{"delete"}
	if force {
		args = append(args, "-f")
	}

	args = append(args, namesOrIDs...)
	_, err := asf.commandContextOutput(ctx, args...)

	return err
}

func (asf *appleShellFrontend) ContainerRun(ctx context.Context, containers ...ContainerRun) error {
	var err error

	for _, container := range containers {
		args := make([]string, 0, 32)
		args = append(args, "run")

		if container.Privileged {
			args = append(args, "--cap-add", "ALL")
		}

		for k, v := range container.Envs {
			env := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "--env", env)
		}

		for k, v := range container.Labels {
			label := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "--label", label)
		}

		for _, mnt := range container.Mounts {
			mount := fmt.Sprintf("type=%s,source=%s,target=%s", mnt.Type, mnt.Source, mnt.Dest)
			if mnt.ReadOnly {
				mount += ",readonly"
			}

			args = append(args, "--mount", mount)
		}

		for _, prt := range container.Ports {
			hostPort := strconv.FormatInt(int64(prt.HostPort), 10)
			if prt.HostPort <= 0 {
				hostPort = ""
			}

			port := fmt.Sprintf("%s:%v:%v", prt.IP, hostPort, prt.ContainerPort)

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

		_, cmdErr := asf.commandContextOutput(ctx, args...)
		if cmdErr != nil {
			err = multierror.Append(err, cmdErr)
		}
	}

	return err
}

func (asf *appleShellFrontend) ImageInfo(ctx context.Context, refs ...string) (map[string]*ImageInfo, error) {
	infos := map[string]*ImageInfo{}
	for _, ref := range refs {
		infos[ref] = &ImageInfo{}
	}

	if len(refs) == 0 {
		return infos, nil
	}

	args := append([]string{"image", "inspect"}, refs...) //nolint:goconst
	output, _ := asf.commandContextOutput(ctx, args...)

	if strings.TrimSpace(output.stdout.String()) == "" {
		return infos, nil
	}

	var inspects []appleImageInspect

	err := json.Unmarshal([]byte(output.stdout.String()), &inspects)
	if err != nil {
		var singleInspect appleImageInspect

		singleErr := json.Unmarshal([]byte(output.stdout.String()), &singleInspect)
		if singleErr == nil {
			inspects = []appleImageInspect{singleInspect}
		} else {
			return nil, errors.Wrapf(err, "failed to decode apple image inspect output: %s", output.stdout.String())
		}
	}

	for i, v := range inspects {
		if i >= len(refs) {
			break
		}

		var osStr, archStr string
		if len(v.Variants) > 0 {
			osStr = v.Variants[0].Platform.OS
			archStr = v.Variants[0].Platform.Architecture
		}

		infos[refs[i]] = &ImageInfo{
			ID:           v.ID,
			OS:           osStr,
			Architecture: archStr,
			Tags:         []string{v.Configuration.Name},
		}
	}

	return infos, nil
}

func (asf *appleShellFrontend) ImagePull(ctx context.Context, refs ...string) error {
	var err error

	for _, ref := range refs {
		args := []string{"image", "pull"}
		if strings.HasPrefix(ref, asf.urls.LocalRegistryHost.Host+"/") {
			args = append(args, "--scheme", "http")
		}

		args = append(args, ref)

		_, cmdErr := asf.commandContextOutput(ctx, args...)
		if cmdErr != nil {
			err = multierror.Append(err, cmdErr)
		}
	}

	return err
}

func (asf *appleShellFrontend) ImageTag(ctx context.Context, tags ...ImageTag) error {
	var err error

	for _, tag := range tags {
		_, cmdErr := asf.commandContextOutput(ctx, "image", "tag", tag.SourceRef, tag.TargetRef)
		if cmdErr != nil {
			err = multierror.Append(err, cmdErr)
		}
	}

	return err
}

func (asf *appleShellFrontend) ImageLoadFromFileCommand(filename string) string {
	binary, args := asf.commandContextStrings("image", "load", "--input", filename)
	all := append([]string{binary}, args...)

	return strings.Join(all, " ")
}

func (asf *appleShellFrontend) ImageLoad(ctx context.Context, images ...io.Reader) error {
	var err error

	for _, image := range images {
		file, tmpErr := os.CreateTemp("", "earthly-apple-load-*")
		if tmpErr != nil {
			err = multierror.Append(err, errors.Wrap(tmpErr, "failed to create temp tarball"))
			continue
		}

		_, copyErr := io.Copy(file, image)
		if copyErr != nil {
			err = multierror.Append(err, errors.Wrapf(tmpErr, "failed to write to %s", file.Name()))
			continue
		}
		defer file.Close()
		defer os.Remove(file.Name())

		output, cmdErr := asf.commandContextOutput(ctx, "image", "load", "--input", file.Name())
		if cmdErr != nil {
			err = multierror.Append(err, errors.Wrapf(cmdErr, "image load failed: %s", output.string()))
		}
	}

	return err
}

func (asf *appleShellFrontend) VolumeInfo(ctx context.Context, volumeNames ...string) (map[string]*VolumeInfo, error) {
	results := map[string]*VolumeInfo{}
	for _, name := range volumeNames {
		results[name] = &VolumeInfo{Name: name}
	}

	if len(volumeNames) == 0 {
		return results, nil
	}

	args := append([]string{"volume", "inspect"}, volumeNames...)

	output, _ := asf.commandContextOutput(ctx, args...)

	if strings.TrimSpace(output.stdout.String()) == "" {
		return results, nil
	}

	var inspects []appleVolumeInspect

	err := json.Unmarshal([]byte(output.stdout.String()), &inspects)
	if err != nil {
		var singleInspect appleVolumeInspect

		singleErr := json.Unmarshal([]byte(output.stdout.String()), &singleInspect)
		if singleErr == nil {
			inspects = []appleVolumeInspect{singleInspect}
		} else {
			return results, errors.Wrapf(err, "failed to decode apple volume inspect output for %v", volumeNames)
		}
	}

	for _, v := range inspects {
		name := v.Configuration.Name
		if name == "" {
			name = v.ID
		}

		vi := &VolumeInfo{
			Name:       name,
			SizeBytes:  v.Configuration.SizeInBytes,
			Mountpoint: v.Configuration.Source,
		}
		for _, reqName := range volumeNames {
			if reqName == name || reqName == v.ID {
				results[reqName] = vi
			}
		}
	}

	return results, nil
}
