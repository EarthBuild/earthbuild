package earthfile2llb

import (
	"context"
	"fmt"
	"path"
	"strings"

	debuggercommon "github.com/EarthBuild/earthbuild/debugger/common"
	"github.com/EarthBuild/earthbuild/util/llbutil"
	"github.com/EarthBuild/earthbuild/util/oidcutil"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"gopkg.in/yaml.v3"
)

const (
	dockerdWrapperPath          = "/var/earthbuild/dockerd-wrapper.sh"
	dockerAutoInstallScriptPath = "/var/earthbuild/docker-auto-install.sh"
	composeConfigFile           = "compose-config.yml"
	startComposeFlag            = "--start-compose"
	suggestedDINDImage          = "earthbuild/dind:alpine-3.24-docker-29.5.3-r0"
)

// DockerLoadOpt holds parameters for WITH DOCKER --load parameter.
type DockerLoadOpt struct {
	Target          string
	ImageName       string
	Platform        platutil.Platform
	BuildArgs       []string
	AllowPrivileged bool
	PassArgs        bool
}

// DockerPullOpt holds parameters for the WITH DOCKER --pull parameter.
type DockerPullOpt struct {
	Platform  platutil.Platform
	ImageName string
}

// WithDockerOpt holds parameters for WITH DOCKER run.
type WithDockerOpt struct {
	OIDCInfo              *oidcutil.AWSOIDCInfo
	CacheID               string
	Pulls                 []DockerPullOpt
	Secrets               []string
	extraRunOpts          []llb.RunOption
	Mounts                []string
	TryCatchSaveArtifacts []debuggercommon.SaveFilesSettings
	ComposeServices       []string
	ComposeFiles          []string
	Loads                 []DockerLoadOpt
	WithSSH               bool
	WithAWSCredentials    bool
	interactiveKeep       bool
	Interactive           bool
	NoCache               bool
	WithEntrypoint        bool
	WithShell             bool
}

type withDockerRunBase struct {
	c *Converter
}

func (w *withDockerRunBase) installDeps(ctx context.Context, opt WithDockerOpt) error {
	installFlag := "--no-start-compose"
	if len(opt.ComposeFiles) > 0 {
		installFlag = startComposeFlag
	}

	args := shellCmd(fmt.Sprintf("%s %s", dockerAutoInstallScriptPath, installFlag))

	prefix, _, err := w.c.newVertexMeta(ctx, false, false, false, opt.Secrets)
	if err != nil {
		return err
	}

	runOpts := []llb.RunOption{
		llb.AddMount(
			dockerAutoInstallScriptPath, llb.Scratch(), llb.HostBind(), llb.SourcePath(dockerAutoInstallScriptPath),
		),
		llb.Args(args),
		llb.WithCustomNamef("%sWITH DOCKER (install deps)", prefix),
	}
	w.c.mts.Final.MainState = w.c.mts.Final.MainState.Run(runOpts...).Root()

	return nil
}

func (w *withDockerRunBase) getComposePulls(ctx context.Context, opt WithDockerOpt) ([]DockerPullOpt, error) {
	if len(opt.ComposeFiles) == 0 {
		// Quick way out. Compose not used.
		return nil, nil
	}
	// Get compose images from compose config.
	composeConfigDt, err := w.getComposeConfig(ctx, opt)
	if err != nil {
		return nil, err
	}

	type composeService struct {
		Image    string `yaml:"image"`
		Platform string `yaml:"platform"`
	}

	type composeData struct {
		Services map[string]composeService `yaml:"services"`
	}

	var config composeData

	err = yaml.Unmarshal(composeConfigDt, &config)
	if err != nil {
		return nil, fmt.Errorf("parse compose config for %v: %w", opt.ComposeFiles, err)
	}

	// Collect relevant images from the compose config.
	composeServicesSet := make(map[string]struct{})
	for _, composeService := range opt.ComposeServices {
		composeServicesSet[composeService] = struct{}{}
	}

	var pulls []DockerPullOpt

	for serviceName, serviceInfo := range config.Services {
		if serviceInfo.Image == "" {
			// Image not specified in yaml.
			continue
		}

		platform := w.c.platr.Current()

		if serviceInfo.Platform != "" {
			p, err := platforms.Parse(serviceInfo.Platform)
			if err != nil {
				return nil, fmt.Errorf("parse platform for image %s: %s: %w", serviceInfo.Image, serviceInfo.Platform, err)
			}

			platform = platutil.FromLLBPlatform(p)
		}

		if len(opt.ComposeServices) > 0 {
			if _, ok := composeServicesSet[serviceName]; ok {
				pulls = append(pulls, DockerPullOpt{
					ImageName: serviceInfo.Image,
					Platform:  platform,
				})
			}
		} else {
			// No services specified. Special case: collect all.
			pulls = append(pulls, DockerPullOpt{
				ImageName: serviceInfo.Image,
				Platform:  platform,
			})
		}
	}

	return pulls, nil
}

func (w *withDockerRunBase) getComposeConfig(ctx context.Context, opt WithDockerOpt) ([]byte, error) {
	// Add the right run to fetch the docker compose config.
	args := shellCmd(
		fmt.Sprintf(
			"%s get-compose-config %s",
			dockerdWrapperPath,
			strings.Join(composeArgs(opt), " "),
		),
	)

	prefix, _, err := w.c.newVertexMeta(ctx, false, false, false, opt.Secrets)
	if err != nil {
		return nil, err
	}

	runOpts := []llb.RunOption{
		llb.AddMount(
			dockerdWrapperPath, llb.Scratch(), llb.HostBind(), llb.SourcePath(dockerdWrapperPath),
		),
		llb.Args(args),
		llb.WithCustomNamef("%sWITH DOCKER (docker-compose config)", prefix),
	}
	state := w.c.mts.Final.MainState.Run(runOpts...).Root()

	ref, err := llbutil.StateToRef(
		ctx, w.c.opt.GwClient, state, w.c.opt.NoCache,
		w.c.platr, w.c.opt.CacheImports.AsSlice(),
	)
	if err != nil {
		return nil, fmt.Errorf("state to ref compose config: %w", err)
	}

	composeConfigDt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
		Filename: "/tmp/earthbuild/" + composeConfigFile,
	})
	if err != nil {
		return nil, fmt.Errorf("read compose config file: %w", err)
	}

	return composeConfigDt, nil
}

func makeWithDockerdWrapFun(dindID string, tarPaths, imgsWithDigests []string, opt WithDockerOpt) shellWrapFun {
	cacheDataRoot := strings.HasPrefix(dindID, "cache_")
	dockerRoot := path.Join("/var/earthbuild/dind", dindID)

	dockerdArgs := []string{dockerdFlag("--data-root", dockerRoot)}
	if cacheDataRoot {
		dockerdArgs = append(dockerdArgs, "--cache-data")
	}

	for _, tarPath := range tarPaths {
		dockerdArgs = append(dockerdArgs, dockerdFlag("--load-file", tarPath))
	}

	// The digests are not actually used by the wrapper, but they are needed in
	// order to bust the cache in case an image is updated.
	for _, imgWithDigest := range imgsWithDigests {
		dockerdArgs = append(dockerdArgs, dockerdFlag("--image-digest", imgWithDigest))
	}

	dockerdArgs = append(dockerdArgs, composeArgs(opt)...)

	return func(args []string, envVars []string, isWithShell, withDebugger, forceDebugger bool) []string {
		return shellCmd(
			strWithEnvVarsAndDocker(
				args, envVars, dockerdArgs, isWithShell, withDebugger, forceDebugger, false, "", "",
			),
		)
	}
}

func composeArgs(opt WithDockerOpt) []string {
	var args []string
	if len(opt.ComposeFiles) > 0 {
		args = append(args, startComposeFlag)
	}

	for _, composeFile := range opt.ComposeFiles {
		args = append(args, dockerdFlag("--compose-file", composeFile))
	}

	for _, composeService := range opt.ComposeServices {
		args = append(args, dockerdFlag("--compose-service", composeService))
	}

	return args
}

// dockerdFlag renders a --name=value flag for dockerd-wrapper.sh. The result is
// spliced into a /bin/sh -c command line, so the value has to be quoted.
func dockerdFlag(name, value string) string {
	return fmt.Sprintf("%s='%s'", name, escapeShellSingleQuotes(value))
}

func platformIncompatMsg(platr *platutil.Resolver) string {
	currentPlatStr := platr.Materialize(platr.Current()).String()
	nativePlatStr := platr.Materialize(platutil.NativePlatform).String()

	return "running WITH DOCKER as a non-native CPU architecture. This is not supported.\n" +
		fmt.Sprintf("Current platform: %s\n", currentPlatStr) +
		fmt.Sprintf("Native platform of the worker: %s\n", nativePlatStr) +
		fmt.Sprintf("Try using\n\n\tFROM --platform=native %s\n\ninstead.\n", suggestedDINDImage) +
		"You may still --load and --pull images of a different platform.\n"
}
