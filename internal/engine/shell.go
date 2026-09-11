package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/EarthBuild/earthbuild/conslogging"
)

type containerInfoJSON struct {
	ID      string    `json:"Id"`
	Name    string    `json:"Name"`
	Created time.Time `json:"Created"`
	State   struct {
		Status string `json:"Status"`
	} `json:"State"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
		Ports map[string][]struct {
			HostIP   string `json:"HostIP"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
	Config struct {
		Labels map[string]string `json:"Labels"`
		Image  string            `json:"Image"`
	} `json:"Config"`
	Image string `json:"Image"`
}

// shellEngine provides shared shell-execution functionality across CLI-based container engines.
type shellEngine struct {
	Log        *conslogging.ConsoleLogger
	Addrs      Addrs
	BinaryName string
	RunArgs    []string
}

// IsAvailable reports whether the CLI binary can execute successfully.
func (e *shellEngine) IsAvailable(ctx context.Context) bool {
	return e.Command(ctx, "ps").Run() == nil
}

const containerDateFormat = "2006-01-02 15:04:05.999999999 -0700 MST"

// ListContainers lists containers using standard formatting.
func (e *shellEngine) ListContainers(ctx context.Context) ([]Container, error) {
	// The custom format below is supported by Docker and Podman.
	args := []string{"ps", "--format", `{{.ID}},{{.Names}},{{.Status}},{{.Image}},{{.CreatedAt}}`}

	output, err := e.CommandOutput(ctx, args...)
	if err != nil {
		return nil, err
	}

	return parseContainerList(output.Stdout.String())
}

// parseContainerList parses standard container list output.
func parseContainerList(output string) ([]Container, error) {
	ret := []Container{}
	// The Docker & Podman JSON output format differs, so we parse the standard output here.
	lines := strings.SplitSeq(strings.TrimSpace(output), "\n")
	for line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) != 5 {
			continue
		}

		createdAt, err := time.Parse(containerDateFormat, parts[4])
		if err != nil {
			return nil, fmt.Errorf("parse container date: %w", err)
		}

		ret = append(ret, Container{
			ID:      parts[0],
			Name:    parts[1],
			Status:  parts[2],
			Image:   parts[3],
			Created: createdAt,
		})
	}

	return ret, nil
}

// InspectContainers returns information for the given container names or IDs.
func (e *shellEngine) InspectContainers(ctx context.Context, namesOrIDs ...string) ([]Container, error) {
	args := append([]string{"container", "inspect"}, namesOrIDs...) //nolint:goconst

	// Ignore the error. This is because one or more of the provided names or IDs could be missing.
	// This allows for Info to report that the container itself is missing.
	output, _ := e.CommandOutput(ctx, args...)

	stdout := strings.TrimSpace(output.Stdout.String())
	if stdout == "" || stdout == "[]" {
		return nil, nil
	}

	var in []containerInfoJSON

	err := json.Unmarshal([]byte(stdout), &in)
	if err != nil {
		return nil, fmt.Errorf("unmarshal container inspect output %s: %w", stdout, err)
	}

	containers := make([]Container, 0, len(in))
	for _, container := range in {
		ipAddresses := map[string]string{}
		for k, v := range container.NetworkSettings.Networks {
			ipAddresses[k] = v.IPAddress
		}

		containers = append(containers, Container{
			ID:      container.ID,
			Name:    strings.TrimPrefix(container.Name, "/"),
			Created: container.Created,
			Status:  container.State.Status,
			IPs:     ipAddresses,
			Labels:  container.Config.Labels,
			Image:   container.Config.Image,
			ImageID: container.Image,
		})
	}

	return containers, nil
}

// RemoveContainer removes the requested containers.
func (e *shellEngine) RemoveContainer(ctx context.Context, force bool, namesOrIDs ...string) error {
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}

	args = append(args, namesOrIDs...)
	_, err := e.CommandOutput(ctx, args...)

	return err
}

// StopContainer stops the requested containers.
func (e *shellEngine) StopContainer(ctx context.Context, timeout time.Duration, namesOrIDs ...string) error {
	args := []string{"stop"}

	if timeout > 0 {
		timeoutSec := max(1, int(timeout.Seconds()))

		args = append(args, "--time", strconv.Itoa(timeoutSec))
	}

	args = append(args, namesOrIDs...)
	_, err := e.CommandOutput(ctx, args...)

	return err
}

// ContainerLogs returns stdout and stderr logs for the requested containers.
func (e *shellEngine) ContainerLogs(ctx context.Context, namesOrIDs ...string) ([]Logs, error) {
	logs := make([]Logs, len(namesOrIDs))

	var err error

	for i, nameOrID := range namesOrIDs {
		// Don't use the wrapper so we can capture stderr and stdout individually
		cmd := e.Command(ctx, "logs", nameOrID)

		var stdout, stderr strings.Builder

		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		cmdErr := cmd.Run()
		if cmdErr != nil {
			err = errors.Join(err, fmt.Errorf("get logs for container %s: %w", nameOrID, cmdErr))
			continue
		}

		logs[i] = Logs{
			Stdout: stdout.String(),
			Stderr: stderr.String(),
		}
	}

	return logs, err
}

// RunContainer runs containers via the CLI.
func (e *shellEngine) RunContainer(ctx context.Context, specs ...ContainerSpec) error {
	var err error

	for _, spec := range specs {
		args := []string{"run"}
		if spec.Privileged {
			args = append(args, "--privileged")
		}

		for k, v := range spec.Envs {
			env := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "-e", env)
		}

		for k, v := range spec.Labels {
			label := fmt.Sprintf("%s=%s", k, v)
			args = append(args, "--label", label)
		}

		for _, m := range spec.Mounts {
			mount := fmt.Sprintf("type=%s,src=%s,dst=%s", m.Type, m.Source, m.Dest)
			if m.ReadOnly {
				mount += ",readonly"
			}

			args = append(args, "--mount", mount)
		}

		for _, portMapping := range spec.PortMappings {
			args = append(args, "-p", portMapping.String())
		}

		args = append(args, "-d")
		args = append(args, "--name", spec.NameOrID)
		args = append(args, spec.AdditionalArgs...)
		args = append(args, e.RunArgs...)
		args = append(args, spec.ImageRef)
		args = append(args, spec.ContainerArgs...)

		_, runErr := e.CommandOutput(ctx, args...)
		if runErr != nil {
			err = errors.Join(err, fmt.Errorf("run container %s: %w", spec.NameOrID, runErr))
		}
	}

	return err
}

// InspectImages returns metadata for the given image references using CLI image inspect.
func (e *shellEngine) InspectImages(ctx context.Context, refs ...string) ([]Image, error) {
	args := append([]string{"image", "inspect"}, refs...) //nolint:goconst

	// Ignore the error. This is because one or more of the provided refs could be missing.
	// This allows for Info to report that the image itself is missing.
	output, _ := e.CommandOutput(ctx, args...)

	stdout := strings.TrimSpace(output.Stdout.String())
	if stdout == "" || stdout == "[]" {
		return nil, nil
	}

	type imageInfoJSON struct {
		ID           string   `json:"Id"`
		Architecture string   `json:"Architecture"`
		OS           string   `json:"Os"`
		RepoTags     []string `json:"RepoTags"`
	}

	var in []imageInfoJSON

	err := json.Unmarshal([]byte(stdout), &in)
	if err != nil {
		return nil, fmt.Errorf("parse image info: %w", err)
	}

	images := make([]Image, 0, len(in))
	for _, img := range in {
		images = append(images, Image{
			ID:           img.ID,
			Architecture: img.Architecture,
			OS:           img.OS,
			Tags:         img.RepoTags,
		})
	}

	return images, nil
}

// PullImage pulls images via the CLI.
func (e *shellEngine) PullImage(ctx context.Context, refs ...string) error {
	var err error

	for _, ref := range refs {
		_, pullErr := e.CommandOutput(ctx, "pull", ref)
		if pullErr != nil {
			err = errors.Join(err, fmt.Errorf("pull image %s: %w", ref, pullErr))
		}
	}

	return err
}

// RemoveImage deletes images via the CLI.
func (e *shellEngine) RemoveImage(ctx context.Context, force bool, refs ...string) error {
	args := []string{"rmi"}
	if force {
		args = append(args, "-f")
	}

	args = append(args, refs...)
	_, err := e.CommandOutput(ctx, args...)

	return err
}

// TagImage tags an image via the CLI.
func (e *shellEngine) TagImage(ctx context.Context, source, target string) error {
	_, err := e.CommandOutput(ctx, "tag", source, target)
	if err != nil {
		return fmt.Errorf("tag image %s -> %s: %w", source, target, err)
	}

	return nil
}

type commandContextOutput struct {
	Stdout strings.Builder
	Stderr strings.Builder
}

func (cco *commandContextOutput) String() string {
	return strings.TrimSpace(cco.Stdout.String() + cco.Stderr.String())
}

// CommandOutput runs an engine command and returns its output, logging execution details if verbose is enabled.
func (e *shellEngine) CommandOutput(ctx context.Context, args ...string) (*commandContextOutput, error) {
	output := &commandContextOutput{}
	cmd := e.Command(ctx, args...)
	e.Log.VerbosePrintf("Running command: %s\n", strings.Join(cmd.Args, " "))

	cmd.Stdout = &output.Stdout
	cmd.Stderr = &output.Stderr

	err := cmd.Run()
	if err != nil {
		out := output.String()
		if out != "" {
			return output, fmt.Errorf("command failed: %s (%s): %w", strings.Join(cmd.Args, " "), out, err)
		}

		return output, fmt.Errorf("command failed: %s: %w", strings.Join(cmd.Args, " "), err)
	}

	return output, nil
}

// Command constructs an *exec.Cmd configured for this engine.
func (e *shellEngine) Command(ctx context.Context, args ...string) *exec.Cmd {
	fullArgs := e.CommandArgs(args...)
	cmd := exec.CommandContext(ctx, fullArgs[0], fullArgs[1:]...) // #nosec G204
	cmd.Env = os.Environ()

	return cmd
}

// CommandArgs generates the full command argument slice with binary name.
func (e *shellEngine) CommandArgs(args ...string) []string {
	return append([]string{e.BinaryName}, args...)
}
