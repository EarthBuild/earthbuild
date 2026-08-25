package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/dustin/go-humanize"
	_ "github.com/moby/buildkit/client/connhelper/podmancontainer" // Load "podman-container://" helper.
)

// podmanEngine implements Engine for the Podman CLI.
type podmanEngine struct {
	*shellEngine
}

// newPodmanEngine constructs a new Engine using the podman binary installed on the host.
func newPodmanEngine(ctx context.Context, cfg *Config) (engineDriver, error) {
	e := &podmanEngine{
		shellEngine: &shellEngine{
			BinaryName:              "podman",
			RunCompatibilityArgs:    []string{"--security-opt", "unmask=/sys/fs/cgroup"},
			GlobalCompatibilityArgs: make([]string, 0),
			Log:                     cfg.Log,
		},
	}

	output, err := e.CommandOutput(ctx, "info", "--format={{.Host.Security.Rootless}}")
	if err != nil {
		return nil, err
	}

	if output.Stderr.Len() > 0 {
		// Only check stdout; since some podman versions less than 3.4 will report warnings about no systemd session,
		// and falling back to cgroupfs. These errors land on stderr. https://github.com/containers/podman/pull/12834
		cfg.Log.VerbosePrintf("Podman logged additional information to stderr:")
		cfg.Log.VerbosePrint(output.Stderr.String())
		cfg.Log.VerbosePrintf("Adding log level compatibility flag for all additional operations.")

		e.GlobalCompatibilityArgs = append(e.GlobalCompatibilityArgs, "--log-level", "error")
	}

	// Only check stdout here since it may be contaminated with log output detected above.
	trimmedStdOut := strings.TrimSpace(output.Stdout.String())

	isRootless, err := strconv.ParseBool(trimmedStdOut)
	if err != nil {
		return nil, fmt.Errorf("info returned invalid value %s: %w", output.String(), err)
	}

	e.Rootless = isRootless

	e.Endpoints, err = e.ResolveEndpoints(PodmanShell, cfg)
	if err != nil {
		return nil, fmt.Errorf("calculate buildkit URLs: %w", err)
	}

	return e, nil
}

// Metadata returns current engine metadata.
func (e *podmanEngine) Metadata() Metadata {
	return Metadata{
		Name:      "Podman",
		Scheme:    SchemePodman,
		Binary:    e.BinaryName,
		Transport: TransportShell,
		Endpoints: e.Endpoints,
		IsPodman:  true,
	}
}

// Version returns version and platform information for the Podman CLI and daemon.
func (e *podmanEngine) Version(ctx context.Context) (Version, error) {
	output, err := e.CommandOutput(ctx, "info", "--format={{.Host.RemoteSocket.Exists}}")
	if err != nil {
		return Version{}, err
	}

	hasRemote, err := strconv.ParseBool(output.String())
	if err != nil {
		return Version{}, fmt.Errorf("info returned invalid value %s: %w", output.String(), err)
	}

	args := []string{"version", "--format=json"}
	if hasRemote {
		args = append([]string{"-r"}, args...)
	}

	output, err = e.CommandOutput(ctx, args...)
	if err != nil {
		// Podman 5.x might return true for .Host.RemoteSocket.Exists but the socket isn't running.
		// Fallback to local version.
		if hasRemote {
			hasRemote = false

			output, err = e.CommandOutput(ctx, "version", "--format=json")
			if err != nil {
				return Version{}, err
			}
		} else {
			return Version{}, err
		}
	}

	type versionInfoJSON struct {
		Client struct {
			Version    string `json:"Version"`
			APIVersion string `json:"APIVersion"`
			Os         string `json:"Os"`
			Arch       string `json:"Arch"`
		} `json:"Client"`
		Server struct {
			Version    string `json:"Version"`
			APIVersion string `json:"APIVersion"`
			Os         string `json:"Os"`
			Arch       string `json:"Arch"`
		} `json:"Server"`
	}

	v := versionInfoJSON{}

	err = json.Unmarshal([]byte(output.Stdout.String()), &v)
	if err != nil {
		return Version{}, fmt.Errorf("parse podman version output %s: %w", output.Stdout.String(), err)
	}

	remoteAddr := ""

	if hasRemote {
		host, exists := os.LookupEnv("CONTAINER_HOST")
		if exists {
			remoteAddr = host
		}
	}

	return Version{
		ClientVersion:    v.Client.Version,
		ClientAPIVersion: v.Client.APIVersion,
		ClientPlatform:   fmt.Sprintf("%s/%s", v.Client.Os, v.Client.Arch),
		ServerVersion:    v.Server.Version,
		ServerAPIVersion: v.Server.APIVersion,
		ServerPlatform:   fmt.Sprintf("%s/%s", v.Server.Os, v.Server.Arch),
		ServerAddress:    remoteAddr,
	}, nil
}

// PullImage downloads the specified container images.
func (e *podmanEngine) PullImage(ctx context.Context, refs ...string) error {
	var err error

	for _, ref := range refs {
		args := []string{"pull"}
		if strings.HasPrefix(ref, e.Endpoints.LocalRegistryHost.Host+"/") {
			// Rather than force users to add an exemption locally in /etc/containers/registries.conf, detect when we are
			// pulling from our own internal registry and manually exempt it from TLS.
			args = append(args, "--tls-verify=false")
		}

		args = append(args, ref)

		_, cmdErr := e.CommandOutput(ctx, args...)
		if cmdErr != nil {
			err = errors.Join(err, fmt.Errorf("pull image %s: %w", ref, cmdErr))
		}
	}

	return err
}

// ImageLoadCommand returns the shell command to load an image from a file.
func (e *podmanEngine) ImageLoadCommand(filename string) string {
	return strings.Join(e.CommandArgs("pull", "docker-archive:"+filename), " ")
}

// LoadImage writes the image to a temp file and pulls it into Podman.
func (e *podmanEngine) LoadImage(ctx context.Context, images ...io.Reader) error {
	var err error

	for _, image := range images {
		loadErr := func() error {
			// Write the image to a temp file. This is needed to accommodate some Podman versions between 3.0 and 3.4. Because
			// buildkit creates weird hybrid docker/OCI images, Podman pulls it in as an OCI image and ends up neglecting the
			// in-built image tag. We can get around this by "pulling" a tar file and specifying the format at the CLI. This
			// is more or less what Podman will be doing going forward. For further context, see the linked issues and discussion
			// here: https://github.com/earthly/earthly/issues/1285
			file, tmpErr := os.CreateTemp("", "earth-podman-load-*")
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

			output, cmdErr := e.CommandOutput(ctx, "pull", "docker-archive:"+file.Name())
			if cmdErr != nil {
				return fmt.Errorf("image load failed: %s: %w", output.String(), cmdErr)
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
func (e *podmanEngine) InspectVolumes(ctx context.Context, volumeNames ...string) ([]Volume, error) {
	// Older podman versions do no support --format. This means we are stuck parsing the verbose tabular output for compat.
	output, err := e.CommandOutput(ctx, "system", "df", "-v")
	if err != nil {
		return nil, err
	}

	idx := strings.Index(output.String(), "Local Volumes space usage:")
	val := output.String()[idx:] //nolint:gocritic
	lines := strings.Split(val, "\n")[3:]
	volumes := make([]Volume, 0, len(volumeNames))

	for _, line := range lines {
		lineParts := strings.Fields(line)
		// There are three columns. By index:
		// 0 -> name, 1 -> links, 2 -> size
		// There may be straggler lines after due to parsing, ignore them. They will not have enough length.
		// The volume lines are last so we are safe.
		if len(lineParts) == 3 && slices.Contains(volumeNames, lineParts[0]) {
			volumeName := lineParts[0]

			bytes, parseErr := humanize.ParseBytes(lineParts[2])
			if parseErr != nil {
				err = errors.Join(err, fmt.Errorf("parse volume size %q for %s: %w", lineParts[2], volumeName, parseErr))
				continue
			}

			// The mountpoint is not included in the df output. Get that from inspect.
			mountpoint, mountpointErr := e.
				CommandOutput(ctx, "volume", "inspect", volumeName, "--format={{.Mountpoint}}")
			if mountpointErr != nil {
				err = errors.Join(err, fmt.Errorf("inspect mountpoint for volume %s: %w", volumeName, mountpointErr))
				continue
			}

			volumes = append(volumes, Volume{
				Name:       volumeName,
				SizeBytes:  bytes,
				Mountpoint: mountpoint.String(),
			})
		}
	}

	return volumes, err
}
