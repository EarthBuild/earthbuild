package flag

import (
	"cmp"
	"os"
	"time"

	"github.com/EarthBuild/earthbuild/buildkitd"
	"github.com/EarthBuild/earthbuild/cmd/earthly/common"
	"github.com/EarthBuild/earthbuild/util/containerutil"
	"github.com/urfave/cli/v3"
)

const (
	// DefaultBuildkitdContainerSuffix is the suffix of the buildkitd container.
	DefaultBuildkitdContainerSuffix = "-buildkitd"

	// DefaultBuildkitdVolumeSuffix is the suffix of the docker volume used for storing the cache.
	DefaultBuildkitdVolumeSuffix = "-cache"

	// DefaultEnvFile is the default path to the env file.
	DefaultEnvFile = ".env"

	// EnvFileFlag is the flag for the env file path.
	EnvFileFlag = "env-file-path"

	// DefaultArgFile is the default path to the arg file.
	DefaultArgFile = ".arg"

	// ArgFileFlag is the flag for the arg file path.
	ArgFileFlag = "arg-file-path"

	// DefaultSecretFile is the default path to the secret file.
	DefaultSecretFile = ".secret"

	// SecretFileFlag is the flag for the secret file path.
	SecretFileFlag = "secret-file-path"
)

// Global flags on Flags instead as there are other things in the CLI that are being called + set
// by the subcommands so I thought it made since to declare them just once there and then
// pass them in.
type Global struct {
	FeatureFlagOverrides       string
	InstallationName           string
	GitUsernameOverride        string
	GitPasswordOverride        string
	GitBranchOverride          string
	ExecStatsSummary           string
	SSHAuthSock                string
	ArgFile                    string
	EnvFile                    string
	LocalRegistryHost          string
	ConfigPath                 string
	DockerfilePath             string
	RemoteCache                string
	SecretFile                 string
	LocalSkipDB                string
	LogstreamDebugFile         string
	LogstreamDebugManifestFile string
	GitLFSPullInclude          string
	BuildkitHost               string
	BuildkitdImage             string
	ContainerName              string
	ContainerFrontend          containerutil.ContainerFrontend
	BuildkitdSettings          buildkitd.Settings
	ServerConnTimeout          time.Duration
	ConversionParallelism      int
	InteractiveDebugging       bool
	NoCache                    bool
	NoBuildkitUpdate           bool
	DisplayExecStats           bool
	Debug                      bool
	ArtifactMode               bool
	ImageMode                  bool
	Pull                       bool
	Push                       bool
	CI                         bool
	UseTickTockBuildkitImage   bool
	Output                     bool
	NoOutput                   bool
	BootstrapNoBuildkit        bool
	SkipBuildkit               bool
	AllowPrivileged            bool
	MaxRemoteCache             bool
	SaveInlineCache            bool
	UseInlineCache             bool
	NoFakeDep                  bool
	Strict                     bool
	GlobalWaitEnd              bool
	Verbose                    bool
	EnableProfiler             bool
	DisableRemoteRegistryProxy bool
	NoAutoSkip                 bool
	GithubAnnotations          bool
}

// RootFlags returns the root flags for the CLI.
func (global *Global) RootFlags(installName string, bkImage string) []cli.Flag {
	defaultInstallationName := cmp.Or(installName, "earthly")

	return []cli.Flag{
		&cli.StringFlag{
			Name:    "installation-name",
			Value:   defaultInstallationName,
			Sources: EarthEnvVars("INSTALLATION_NAME"),
			Usage: "The earth installation name to use when naming the buildkit container, " +
				"the docker volume and the ~/.earthly directory",
			Destination: &global.InstallationName,
			Hidden:      true, // Internal.
		},
		&cli.StringFlag{
			Name:        "config",
			Value:       "", // the default value will be applied in the "Before" fn, after flag.installationName is set.
			Sources:     EarthEnvVars("CONFIG"),
			Usage:       "Path to config file",
			Destination: &global.ConfigPath,
		},
		&cli.StringFlag{
			Name:        "ssh-auth-sock",
			Value:       os.Getenv("SSH_AUTH_SOCK"),
			Sources:     EarthEnvVars("SSH_AUTH_SOCK"),
			Usage:       "The SSH auth socket to use for ssh-agent forwarding",
			Destination: &global.SSHAuthSock,
		},
		&cli.StringFlag{
			Name:        "git-username",
			Sources:     cli.EnvVars("GIT_USERNAME"),
			Usage:       "The git username to use for git HTTPS authentication",
			Destination: &global.GitUsernameOverride,
		},
		&cli.StringFlag{
			Name:        "git-password",
			Sources:     cli.EnvVars("GIT_PASSWORD"),
			Usage:       "The git password to use for git HTTPS authentication",
			Destination: &global.GitPasswordOverride,
		},
		&cli.StringFlag{
			Name:        "git-branch",
			Sources:     EarthEnvVars("GIT_BRANCH_OVERRIDE"),
			Usage:       "The git branch the build should be considered running in",
			Destination: &global.GitBranchOverride,
			Hidden:      true, // primarily used by CI to pass branch context
		},
		&cli.BoolFlag{
			Name:        "verbose",
			Aliases:     []string{"V"},
			Sources:     EarthEnvVars("VERBOSE"),
			Usage:       "Enable verbose logging",
			Destination: &global.Verbose,
		},
		&cli.BoolFlag{
			Name:    "debug",
			Aliases: []string{"D"},
			Sources: EarthEnvVars("DEBUG"),
			Usage: "Enable debug mode. This flag also turns on the debug mode of buildkitd, " +
				"which may cause it to restart",
			Destination: &global.Debug,
			Hidden:      true, // For development purposes only.
		},
		&cli.BoolFlag{
			Name:        "exec-stats",
			Sources:     EarthEnvVars("EXEC_STATS"),
			Usage:       "Display container stats (e.g. cpu and memory usage)",
			Destination: &global.DisplayExecStats,
			Hidden:      true, // Experimental
		},
		&cli.StringFlag{
			Name:        "exec-stats-summary",
			Sources:     EarthEnvVars("EXEC_STATS_SUMMARY"),
			Usage:       "Output summarized container stats (e.g. cpu and memory usage) to the specified file",
			Destination: &global.ExecStatsSummary,
			Hidden:      true, // Experimental
		},
		&cli.BoolFlag{
			Name:        "profiler",
			Sources:     EarthEnvVars("PROFILER"),
			Usage:       "Enable the profiler",
			Destination: &global.EnableProfiler,
			Hidden:      true, // Dev purposes only.
		},
		&cli.StringFlag{
			Name:    "buildkit-host",
			Value:   "",
			Sources: EarthEnvVars("BUILDKIT_HOST"),
			Usage: `The URL to use for connecting to a buildkit host
		If empty, earth will attempt to start a buildkitd instance via docker run`,
			Destination: &global.BuildkitHost,
		},
		&cli.BoolFlag{
			Name:        "no-buildkit-update",
			Sources:     EarthEnvVars("NO_BUILDKIT_UPDATE"),
			Usage:       "Disable the automatic update of buildkitd",
			Destination: &global.NoBuildkitUpdate,
			Hidden:      true, // Internal.
		},
		&cli.StringFlag{
			Name:    "version-flag-overrides",
			Sources: EarthEnvVars("VERSION_FLAG_OVERRIDES"),
			Usage: "Apply additional flags after each VERSION command across all Earthfiles, " +
				"multiple flags can be separated by commas",
			Destination: &global.FeatureFlagOverrides,
			Hidden:      true, // used for feature-flipping from ./earthly dev script
		},
		&cli.StringFlag{
			Name:    EnvFileFlag,
			Sources: EarthEnvVars("ENV_FILE_PATH"),
			Usage: "Use values from this file as earth environment variables; " +
				"values are no longer used as --build-arg's or --secret's",
			Value:       DefaultEnvFile,
			Destination: &global.EnvFile,
		},
		&cli.StringFlag{
			Name:        ArgFileFlag,
			Sources:     EarthEnvVars("ARG_FILE_PATH"),
			Usage:       "Use values from this file as earth buildargs",
			Value:       DefaultArgFile,
			Destination: &global.ArgFile,
		},
		&cli.StringFlag{
			Name:        SecretFileFlag,
			Sources:     EarthEnvVars("SECRET_FILE_PATH"),
			Usage:       "Use values from this file as earth secrets",
			Value:       DefaultSecretFile,
			Destination: &global.SecretFile,
		},
		&cli.StringFlag{
			Name:        "logstream-debug-file",
			Sources:     EarthEnvVars("LOGSTREAM_DEBUG_FILE"),
			Usage:       "Enable log streaming debugging output to a file",
			Destination: &global.LogstreamDebugFile,
			Hidden:      true, // Internal.
		},
		&cli.StringFlag{
			Name:        "logstream-debug-manifest-file",
			Sources:     EarthEnvVars("LOGSTREAM_DEBUG_MANIFEST_FILE"),
			Usage:       "Enable log streaming manifest debugging output to a file",
			Destination: &global.LogstreamDebugManifestFile,
			Hidden:      true, // Internal.
		},
		&cli.DurationFlag{
			Name:        "server-conn-timeout",
			Usage:       "EarthBuild API server connection timeout value",
			Sources:     EarthEnvVars("SERVER_CONN_TIMEOUT"),
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
			Sources:     EarthEnvVars("PULL"),
			Usage:       "Force pull any referenced Docker images",
			Destination: &global.Pull,
			Hidden:      true, // Experimental
		},
		&cli.BoolFlag{
			Name:        "push",
			Sources:     EarthEnvVars("PUSH"),
			Usage:       "Push docker images and execute RUN --push commands",
			Destination: &global.Push,
		},
		&cli.BoolFlag{
			Name:        "ci",
			Sources:     EarthEnvVars("CI"),
			Usage:       common.Wrap("Execute in CI mode. ", "Implies --no-output --strict"),
			Destination: &global.CI,
		},
		&cli.BoolFlag{
			Name:        "ticktock",
			Sources:     EarthEnvVars("TICKTOCK"),
			Usage:       "Use earthbuild's experimental buildkit ticktock codebase",
			Destination: &global.UseTickTockBuildkitImage,
			Hidden:      true, // Experimental
		},
		&cli.BoolFlag{
			Name:        "output",
			Sources:     EarthEnvVars("OUTPUT"),
			Usage:       "Allow artifacts or images to be output, even when running under --ci mode",
			Destination: &global.Output,
		},
		&cli.BoolFlag{
			Name:        "no-output",
			Sources:     EarthEnvVars("NO_OUTPUT"),
			Usage:       common.Wrap("Do not output artifacts or images", "(using --push is still allowed)"),
			Destination: &global.NoOutput,
		},
		&cli.BoolFlag{
			Name:        "no-cache",
			Sources:     EarthEnvVars("NO_CACHE"),
			Usage:       "Do not use cache while building",
			Destination: &global.NoCache,
		},
		&cli.BoolFlag{
			Name:        "auto-skip",
			Sources:     EarthEnvVars("AUTO_SKIP"),
			Usage:       "Skip buildkit if target has already been built",
			Destination: &global.SkipBuildkit,
		},
		&cli.BoolFlag{
			Name:        "allow-privileged",
			Aliases:     []string{"P"},
			Sources:     EarthEnvVars("ALLOW_PRIVILEGED"),
			Usage:       "Allow build to use the --privileged flag in RUN commands",
			Destination: &global.AllowPrivileged,
		},
		&cli.BoolFlag{
			Name:        "max-remote-cache",
			Sources:     EarthEnvVars("MAX_REMOTE_CACHE"),
			Usage:       "Saves all intermediate images too in the remote cache",
			Destination: &global.MaxRemoteCache,
		},
		&cli.BoolFlag{
			Name:        "save-inline-cache",
			Sources:     EarthEnvVars("SAVE_INLINE_CACHE"),
			Usage:       "Enable cache inlining when pushing images",
			Destination: &global.SaveInlineCache,
		},
		&cli.BoolFlag{
			Name:    "use-inline-cache",
			Sources: EarthEnvVars("USE_INLINE_CACHE"),
			Usage: common.Wrap("Attempt to use any inline cache that may have been previously pushed ",
				"uses image tags referenced by SAVE IMAGE --push or SAVE IMAGE --cache-from"),
			Destination: &global.UseInlineCache,
		},
		&cli.BoolFlag{
			Name:        "interactive",
			Aliases:     []string{"i"},
			Sources:     EarthEnvVars("INTERACTIVE"),
			Usage:       "Enable interactive debugging",
			Destination: &global.InteractiveDebugging,
		},
		&cli.BoolFlag{
			Name:        "no-fake-dep",
			Sources:     EarthEnvVars("NO_FAKE_DEP"),
			Usage:       "Internal feature flag for fake-dep",
			Destination: &global.NoFakeDep,
			Hidden:      true, // Internal.
		},
		&cli.BoolFlag{
			Name:        "strict",
			Sources:     EarthEnvVars("STRICT"),
			Usage:       "Disallow usage of features that may create unrepeatable builds",
			Destination: &global.Strict,
		},
		&cli.BoolFlag{
			Name:        "global-wait-end",
			Sources:     EarthEnvVars("GLOBAL_WAIT_END"),
			Usage:       "enables global wait-end code in place of builder code",
			Destination: &global.GlobalWaitEnd,
			Hidden:      true, // used to force code-coverage of future builder.go refactor (once we remove support for 0.6)
		},
		&cli.StringFlag{
			Name:    "git-lfs-pull-include",
			Sources: EarthEnvVars("GIT_LFS_PULL_INCLUDE"),
			Usage: "When referencing a remote target, perform a git lfs pull include prior to running the target. " +
				"Note that this flag is (hopefully) temporary, " +
				"see https://github.com/earthly/earthly/issues/2921 for details.",
			Destination: &global.GitLFSPullInclude,
			Hidden:      true, // Experimental
		},
		&cli.StringFlag{
			Name:        "auto-skip-db-path",
			Sources:     EarthEnvVars("AUTO_SKIP_DB_PATH"),
			Usage:       "use a local database for auto-skip",
			Destination: &global.LocalSkipDB,
		},
		&cli.StringFlag{
			Name:        "buildkit-image",
			Value:       bkImage,
			Sources:     EarthEnvVars("BUILDKIT_IMAGE"),
			Usage:       "The docker image to use for the buildkit daemon",
			Destination: &global.BuildkitdImage,
		},
		&cli.StringFlag{
			Name:        "buildkit-container-name",
			Value:       defaultInstallationName + DefaultBuildkitdContainerSuffix,
			Sources:     EarthEnvVars("CONTAINER_NAME"),
			Usage:       "The docker container name to use for the buildkit daemon",
			Destination: &global.ContainerName,
			Hidden:      true,
		},
		&cli.StringFlag{
			Name:        "buildkit-volume-name",
			Value:       defaultInstallationName + DefaultBuildkitdVolumeSuffix,
			Sources:     EarthEnvVars("VOLUME_NAME"),
			Usage:       "The docker volume name to use for the buildkit daemon cache",
			Destination: &global.BuildkitdSettings.VolumeName,
			Hidden:      true,
		},
		&cli.StringFlag{
			Name:    "remote-cache",
			Sources: EarthEnvVars("REMOTE_CACHE"),
			Usage: "A remote docker image tag use as explicit cache and optionally additional attributes " +
				"to set in the image (Format: \"<image-tag>[,<attr1>=<val1>,<attr2>=<val2>,...]\")",
			Destination: &global.RemoteCache,
		},
		&cli.BoolFlag{
			Name:        "disable-remote-registry-proxy",
			Sources:     EarthEnvVars("DISABLE_REMOTE_REGISTRY_PROXY"),
			Usage:       "Don't use the Docker registry proxy when transferring images",
			Destination: &global.DisableRemoteRegistryProxy,
			Value:       false,
		},
		&cli.BoolFlag{
			Name:        "no-auto-skip",
			Sources:     EarthEnvVars("NO_AUTO_SKIP"),
			Usage:       "Disable auto-skip functionality",
			Destination: &global.NoAutoSkip,
			Value:       false,
		},
		&cli.BoolFlag{
			Name:        "github-annotations",
			Sources:     cli.EnvVars("GITHUB_ACTIONS"),
			Usage:       "Enable Git Hub Actions workflow specific output",
			Destination: &global.GithubAnnotations,
			Value:       false,
		},
	}
}
