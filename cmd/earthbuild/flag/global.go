package flag

import (
	"os"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/earthbuild/earthbuild/buildkitd"
	"github.com/earthbuild/earthbuild/cmd/earthbuild/common"
	"github.com/earthbuild/earthbuild/util/containerutil"
)

const (
	// DefaultBuildkitdContainerSuffix is the suffix of the buildkitd container.
	DefaultBuildkitdContainerSuffix = "-buildkitd"
	// DefaultBuildkitdVolumeSuffix is the suffix of the docker volume used for storing the cache.
	DefaultBuildkitdVolumeSuffix = "-cache"

	DefaultEnvFile = ".env"
	EnvFileFlag    = "env-file-path"

	DefaultArgFile = ".arg"
	ArgFileFlag    = "arg-file-path"

	DefaultSecretFile = ".secret"
	SecretFileFlag    = "secret-file-path"
)

// Put flags on Flags instead as there are other things in the CLI that are being called + set
// by the subcommands so I thought it made since to declare them just once there and then
// pass them in
type Global struct {
	DockerfilePath             string
	EnableProfiler             bool
	InstallationName           string
	ConfigPath                 string
	GitUsernameOverride        string
	GitPasswordOverride        string
	GitBranchOverride          string
	ExecStatsSummary           string
	SSHAuthSock                string
	Verbose                    bool
	Debug                      bool
	DisplayExecStats           bool
	FeatureFlagOverrides       string
	EnvFile                    string
	ArgFile                    string
	SecretFile                 string
	NoBuildkitUpdate           bool
	LogstreamDebugFile         string
	LogstreamDebugManifestFile string
	ServerConnTimeout          time.Duration
	BuildkitHost               string
	BuildkitdImage             string
	ContainerName              string
	GitLFSPullInclude          string
	BuildkitdSettings          buildkitd.Settings
	InteractiveDebugging       bool
	BootstrapNoBuildkit        bool
	ConversionParallelism      int
	LocalRegistryHost          string
	ContainerFrontend          containerutil.ContainerFrontend
	ArtifactMode               bool
	ImageMode                  bool
	Pull                       bool
	Push                       bool
	CI                         bool
	UseTickTockBuildkitImage   bool
	Output                     bool
	NoOutput                   bool
	NoCache                    bool
	SkipBuildkit               bool
	AllowPrivileged            bool
	MaxRemoteCache             bool
	SaveInlineCache            bool
	UseInlineCache             bool
	NoFakeDep                  bool
	Strict                     bool
	GlobalWaitEnd              bool
	RemoteCache                string
	LocalSkipDB                string
	DisableRemoteRegistryProxy bool
	NoAutoSkip                 bool
	GithubAnnotations          bool
}

func (global *Global) RootFlags(installName string, bkImage string) []cli.Flag {
	defaultInstallationName := installName
	if defaultInstallationName == "" {
		defaultInstallationName = "earthbuild"
	}
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "installation-name",
			Value:       defaultInstallationName,
			EnvVars:     []string{"earthbuild_INSTALLATION_NAME"},
			Usage:       "The earthbuild installation name to use when naming the buildkit container, the docker volume and the ~/.earthbuild directory",
			Destination: &global.InstallationName,
			Hidden:      true, // Internal.
		},
		&cli.StringFlag{
			Name:        "config",
			Value:       "", // the default value will be applied in the "Before" fn, after flag.installationName is set.
			EnvVars:     []string{"EARTHBUILD_CONFIG"},
			Usage:       "Path to config file",
			Destination: &global.ConfigPath,
		},
		&cli.StringFlag{
			Name:        "ssh-auth-sock",
			Value:       os.Getenv("SSH_AUTH_SOCK"),
			EnvVars:     []string{"earthbuild_SSH_AUTH_SOCK"},
			Usage:       "The SSH auth socket to use for ssh-agent forwarding",
			Destination: &global.SSHAuthSock,
		},
		&cli.StringFlag{
			Name:        "git-username",
			EnvVars:     []string{"GIT_USERNAME"},
			Usage:       "The git username to use for git HTTPS authentication",
			Destination: &global.GitUsernameOverride,
		},
		&cli.StringFlag{
			Name:        "git-password",
			EnvVars:     []string{"GIT_PASSWORD"},
			Usage:       "The git password to use for git HTTPS authentication",
			Destination: &global.GitPasswordOverride,
		},
		&cli.StringFlag{
			Name:        "git-branch",
			EnvVars:     []string{"earthbuild_GIT_BRANCH_OVERRIDE"},
			Usage:       "The git branch the build should be considered running in",
			Destination: &global.GitBranchOverride,
			Hidden:      true, // primarily used by CI to pass branch context
		},
		&cli.BoolFlag{
			Name:        "verbose",
			Aliases:     []string{"V"},
			EnvVars:     []string{"earthbuild_VERBOSE"},
			Usage:       "Enable verbose logging",
			Destination: &global.Verbose,
		},
		&cli.BoolFlag{
			Name:        "debug",
			Aliases:     []string{"D"},
			EnvVars:     []string{"earthbuild_DEBUG"},
			Usage:       "Enable debug mode. This flag also turns on the debug mode of buildkitd, which may cause it to restart",
			Destination: &global.Debug,
			Hidden:      true, // For development purposes only.
		},
		&cli.BoolFlag{
			Name:        "exec-stats",
			EnvVars:     []string{"earthbuild_EXEC_STATS"},
			Usage:       "Display container stats (e.g. cpu and memory usage)",
			Destination: &global.DisplayExecStats,
			Hidden:      true, // Experimental
		},
		&cli.StringFlag{
			Name:        "exec-stats-summary",
			EnvVars:     []string{"earthbuild_EXEC_STATS_SUMMARY"},
			Usage:       "Output summarized container stats (e.g. cpu and memory usage) to the specified file",
			Destination: &global.ExecStatsSummary,
			Hidden:      true, // Experimental
		},
		&cli.BoolFlag{
			Name:        "profiler",
			EnvVars:     []string{"earthbuild_PROFILER"},
			Usage:       "Enable the profiler",
			Destination: &global.EnableProfiler,
			Hidden:      true, // Dev purposes only.
		},
		&cli.StringFlag{
			Name:    "buildkit-host",
			Value:   "",
			EnvVars: []string{"earthbuild_BUILDKIT_HOST"},
			Usage: `The URL to use for connecting to a buildkit host
		If empty, earthbuild will attempt to start a buildkitd instance via docker run`,
			Destination: &global.BuildkitHost,
		},
		&cli.BoolFlag{
			Name:        "no-buildkit-update",
			EnvVars:     []string{"earthbuild_NO_BUILDKIT_UPDATE"},
			Usage:       "Disable the automatic update of buildkitd",
			Destination: &global.NoBuildkitUpdate,
			Hidden:      true, // Internal.
		},
		&cli.StringFlag{
			Name:        "version-flag-overrides",
			EnvVars:     []string{"EARTHLY_VERSION_FLAG_OVERRIDES"},
			Usage:       "Apply additional flags after each VERSION command across all Earthfiles, multiple flags can be separated by commas",
			Destination: &global.FeatureFlagOverrides,
			Hidden:      true, // used for feature-flipping from ./earthbuild dev script
		},
		&cli.StringFlag{
			Name:        EnvFileFlag,
			EnvVars:     []string{"EARTHBUILD_ENV_FILE_PATH"},
			Usage:       "Use values from this file as earthbuild environment variables; values are no longer used as --build-arg's or --secret's",
			Value:       DefaultEnvFile,
			Destination: &global.EnvFile,
		},
		&cli.StringFlag{
			Name:        ArgFileFlag,
			EnvVars:     []string{"earthbuild_ARG_FILE_PATH"},
			Usage:       "Use values from this file as earthbuild buildargs",
			Value:       DefaultArgFile,
			Destination: &global.ArgFile,
		},
		&cli.StringFlag{
			Name:        SecretFileFlag,
			EnvVars:     []string{"earthbuild_SECRET_FILE_PATH"},
			Usage:       "Use values from this file as earthbuild secrets",
			Value:       DefaultSecretFile,
			Destination: &global.SecretFile,
		},
		&cli.StringFlag{
			Name:        "logstream-debug-file",
			EnvVars:     []string{"earthbuild_LOGSTREAM_DEBUG_FILE"},
			Usage:       "Enable log streaming debugging output to a file",
			Destination: &global.LogstreamDebugFile,
			Hidden:      true, // Internal.
		},
		&cli.StringFlag{
			Name:        "logstream-debug-manifest-file",
			EnvVars:     []string{"earthbuild_LOGSTREAM_DEBUG_MANIFEST_FILE"},
			Usage:       "Enable log streaming manifest debugging output to a file",
			Destination: &global.LogstreamDebugManifestFile,
			Hidden:      true, // Internal.
		},
		&cli.DurationFlag{
			Name:        "server-conn-timeout",
			Usage:       "earthbuild API server connection timeout value",
			EnvVars:     []string{"earthbuild_SERVER_CONN_TIMEOUT"},
			Hidden:      true, // Internal.
			Value:       5 * time.Second,
			Destination: &global.ServerConnTimeout,
		},
		&cli.BoolFlag{
			Name:        "artifact",
			Aliases:     []string{"a"},
			Usage:       "Output specified artifact; a wildcard (*) can be used to output all artifacts",
			Destination: &global.ArtifactMode,
		},
		&cli.BoolFlag{
			Name:        "image",
			Usage:       "Output only docker image of the specified target",
			Destination: &global.ImageMode,
		},
		&cli.BoolFlag{
			Name:        "pull",
			EnvVars:     []string{"earthbuild_PULL"},
			Usage:       "Force pull any referenced Docker images",
			Destination: &global.Pull,
			Hidden:      true, // Experimental
		},
		&cli.BoolFlag{
			Name:        "push",
			EnvVars:     []string{"EARTHLY_PUSH"},
			Usage:       "Push docker images and execute RUN --push commands",
			Destination: &global.Push,
		},
		&cli.BoolFlag{
			Name:        "ci",
			EnvVars:     []string{"EARTHLY_CI"},
			Usage:       common.Wrap("Execute in CI mode. ", "Implies --no-output --strict"),
			Destination: &global.CI,
		},
		&cli.BoolFlag{
			Name:        "ticktock",
			EnvVars:     []string{"earthbuild_TICKTOCK"},
			Usage:       "Use earthbuild's experimental buildkit ticktock codebase",
			Destination: &global.UseTickTockBuildkitImage,
			Hidden:      true, // Experimental
		},
		&cli.BoolFlag{
			Name:        "output",
			EnvVars:     []string{"earthbuild_OUTPUT"},
			Usage:       "Allow artifacts or images to be output, even when running under --ci mode",
			Destination: &global.Output,
		},
		&cli.BoolFlag{
			Name:        "no-output",
			EnvVars:     []string{"earthbuild_NO_OUTPUT"},
			Usage:       common.Wrap("Do not output artifacts or images", "(using --push is still allowed)"),
			Destination: &global.NoOutput,
		},
		&cli.BoolFlag{
			Name:        "no-cache",
			EnvVars:     []string{"EARTHBUILD_NO_CACHE"},
			Usage:       "Do not use cache while building",
			Destination: &global.NoCache,
		},
		&cli.BoolFlag{
			Name:        "auto-skip",
			EnvVars:     []string{"EARTHBUILD_AUTO_SKIP"},
			Usage:       "Skip buildkit if target has already been built",
			Destination: &global.SkipBuildkit,
		},
		&cli.BoolFlag{
			Name:        "allow-privileged",
			Aliases:     []string{"P"},
			EnvVars:     []string{"EARTHBUILD_ALLOW_PRIVILEGED"},
			Usage:       "Allow build to use the --privileged flag in RUN commands",
			Destination: &global.AllowPrivileged,
		},
		&cli.BoolFlag{
			Name:        "max-remote-cache",
			EnvVars:     []string{"EARTHBUILD_MAX_REMOTE_CACHE"},
			Usage:       "Saves all intermediate images too in the remote cache",
			Destination: &global.MaxRemoteCache,
		},
		&cli.BoolFlag{
			Name:        "save-inline-cache",
			EnvVars:     []string{"EARTHBUILD_SAVE_INLINE_CACHE"},
			Usage:       "Enable cache inlining when pushing images",
			Destination: &global.SaveInlineCache,
		},
		&cli.BoolFlag{
			Name:        "use-inline-cache",
			EnvVars:     []string{"EARTHBUILD_USE_INLINE_CACHE"},
			Usage:       common.Wrap("Attempt to use any inline cache that may have been previously pushed ", "uses image tags referenced by SAVE IMAGE --push or SAVE IMAGE --cache-from"),
			Destination: &global.UseInlineCache,
		},
		&cli.BoolFlag{
			Name:        "interactive",
			Aliases:     []string{"i"},
			EnvVars:     []string{"EARTHBUILD_INTERACTIVE"},
			Usage:       "Enable interactive debugging",
			Destination: &global.InteractiveDebugging,
		},
		&cli.BoolFlag{
			Name:        "no-fake-dep",
			EnvVars:     []string{"EARTHBUILD_NO_FAKE_DEP"},
			Usage:       "Internal feature flag for fake-dep",
			Destination: &global.NoFakeDep,
			Hidden:      true, // Internal.
		},
		&cli.BoolFlag{
			Name:        "strict",
			EnvVars:     []string{"EARTHBUILD_STRICT"},
			Usage:       "Disallow usage of features that may create unrepeatable builds",
			Destination: &global.Strict,
		},
		&cli.BoolFlag{
			Name:        "global-wait-end",
			EnvVars:     []string{"EARTHBUILD_GLOBAL_WAIT_END"},
			Usage:       "enables global wait-end code in place of builder code",
			Destination: &global.GlobalWaitEnd,
			Hidden:      true, // used to force code-coverage of future builder.go refactor (once we remove support for 0.6)
		},
		&cli.StringFlag{
			Name:        "git-lfs-pull-include",
			EnvVars:     []string{"EARTHBUILD_GIT_LFS_PULL_INCLUDE"},
			Usage:       "When referencing a remote target, perform a git lfs pull include prior to running the target. Note that this flag is (hopefully) temporary, see https://github.com/earthbuild/earthbuild/issues/2921 for details.",
			Destination: &global.GitLFSPullInclude,
			Hidden:      true, // Experimental
		},
		&cli.StringFlag{
			Name:        "auto-skip-db-path",
			EnvVars:     []string{"EARTHBUILD_AUTO_SKIP_DB_PATH"},
			Usage:       "use a local database for auto-skip",
			Destination: &global.LocalSkipDB,
		},
		&cli.StringFlag{
			Name:        "buildkit-image",
			Value:       bkImage,
			EnvVars:     []string{"EARTHBUILD_BUILDKIT_IMAGE"},
			Usage:       "The docker image to use for the buildkit daemon",
			Destination: &global.BuildkitdImage,
		},
		&cli.StringFlag{
			Name:        "buildkit-container-name",
			Value:       defaultInstallationName + DefaultBuildkitdContainerSuffix,
			EnvVars:     []string{"EARTHBUILD_CONTAINER_NAME"},
			Usage:       "The docker container name to use for the buildkit daemon",
			Destination: &global.ContainerName,
			Hidden:      true,
		},
		&cli.StringFlag{
			Name:        "buildkit-volume-name",
			Value:       defaultInstallationName + DefaultBuildkitdVolumeSuffix,
			EnvVars:     []string{"EARTHBUILD_VOLUME_NAME"},
			Usage:       "The docker volume name to use for the buildkit daemon cache",
			Destination: &global.BuildkitdSettings.VolumeName,
			Hidden:      true,
		},
		&cli.StringFlag{
			Name:        "remote-cache",
			EnvVars:     []string{"EARTHBUILD_REMOTE_CACHE"},
			Usage:       "A remote docker image tag use as explicit cache and optionally additional attributes to set in the image (Format: \"<image-tag>[,<attr1>=<val1>,<attr2>=<val2>,...]\")",
			Destination: &global.RemoteCache,
		},
		&cli.BoolFlag{
			Name:        "disable-remote-registry-proxy",
			EnvVars:     []string{"EARTHBUILD_DISABLE_REMOTE_REGISTRY_PROXY"},
			Usage:       "Don't use the Docker registry proxy when transferring images",
			Destination: &global.DisableRemoteRegistryProxy,
			Value:       false,
		},
		&cli.BoolFlag{
			Name:        "no-auto-skip",
			EnvVars:     []string{"EARTHBUILD_NO_AUTO_SKIP"},
			Usage:       "Disable auto-skip functionality",
			Destination: &global.NoAutoSkip,
			Value:       false,
		},
		&cli.BoolFlag{
			Name:        "github-annotations",
			EnvVars:     []string{"GITHUB_ACTIONS"},
			Usage:       "Enable Git Hub Actions workflow specific output",
			Destination: &global.GithubAnnotations,
			Value:       false,
		},
	}
}
