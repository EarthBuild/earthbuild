package subcmd

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EarthBuild/earthbuild/ast"
	"github.com/EarthBuild/earthbuild/buildcontext"
	"github.com/EarthBuild/earthbuild/buildcontext/provider"
	"github.com/EarthBuild/earthbuild/builder"
	"github.com/EarthBuild/earthbuild/buildkitd"
	"github.com/EarthBuild/earthbuild/cleanup"
	"github.com/EarthBuild/earthbuild/cmd/earthly/bk"
	"github.com/EarthBuild/earthbuild/cmd/earthly/common"
	"github.com/EarthBuild/earthbuild/cmd/earthly/flag"
	debuggercommon "github.com/EarthBuild/earthbuild/debugger/common"
	"github.com/EarthBuild/earthbuild/debugger/terminal"
	"github.com/EarthBuild/earthbuild/docker2earth"
	"github.com/EarthBuild/earthbuild/domain"
	"github.com/EarthBuild/earthbuild/inputgraph"
	"github.com/EarthBuild/earthbuild/states"
	"github.com/EarthBuild/earthbuild/util/buildkitskipper"
	"github.com/EarthBuild/earthbuild/util/cliutil"
	"github.com/EarthBuild/earthbuild/util/containerutil"
	"github.com/EarthBuild/earthbuild/util/flagutil"
	"github.com/EarthBuild/earthbuild/util/gatewaycrafter"
	"github.com/EarthBuild/earthbuild/util/gitutil"
	"github.com/EarthBuild/earthbuild/util/llbutil/authprovider"
	"github.com/EarthBuild/earthbuild/util/llbutil/secretprovider"
	"github.com/EarthBuild/earthbuild/util/params"
	"github.com/EarthBuild/earthbuild/util/platutil"
	"github.com/EarthBuild/earthbuild/util/syncutil/semutil"
	"github.com/EarthBuild/earthbuild/util/termutil"
	"github.com/EarthBuild/earthbuild/variables"
	"github.com/containerd/platforms"
	"github.com/docker/cli/cli/config"
	"github.com/joho/godotenv"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth"
	dockerauthprovider "github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/session/localhost/localhostprovider"
	"github.com/moby/buildkit/session/socketforward/socketprovider"
	"github.com/moby/buildkit/session/sshforward/sshprovider"
	"github.com/moby/buildkit/util/entitlements"
	buildkitgitutil "github.com/moby/buildkit/util/gitutil"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v3"
)

const autoSkipPrefix = "auto-skip"

// Build encapsulates the build command logic.
type Build struct {
	cli          CLI
	dockerTarget string
	buildArgs    []string
	platformsStr []string
	secrets      []string
	secretFiles  []string
	cacheFrom    []string
	dockerTags   []string
}

// NewBuild creates a new Build command.
func NewBuild(cli CLI) *Build {
	return &Build{
		cli: cli,
	}
}

// Cmds returns the list of commands for the build command.
func (b *Build) Cmds() []*cli.Command {
	return []*cli.Command{
		{
			Name:         "build",
			Usage:        "Build an earth target",
			Description:  "Build an earth target.",
			Action:       b.Action,
			StopOnNthArg: new(1),
			Flags:        b.buildFlags(),
			Hidden:       true, // Meant to be used mainly for help output.
		},
		{
			Name:  "docker-build",
			Usage: "*beta* Build a Dockerfile without an Earthfile",
			UsageText: "earth [options] docker-build " +
				"[--dockerfile <dockerfile-path>] " +
				"[--tag=<image-tag>] " +
				"[--target=<target-name>] " +
				"[--platform <platform1[,platform2,...]>] " +
				"<build-context-dir> " +
				"[--arg1=arg-value]",
			Description:  "*beta* Builds a Dockerfile without an Earthfile.",
			Action:       b.actionDockerBuild,
			StopOnNthArg: new(1),
			Flags: append(b.buildFlags(),
				&cli.StringFlag{
					Name:        "dockerfile",
					Aliases:     []string{"f"},
					Sources:     cli.EnvVars("EARTHLY_DOCKER_FILE"),
					Usage:       "Path to dockerfile input",
					Value:       "Dockerfile",
					Destination: &b.cli.Flags().DockerfilePath,
				},
				&cli.StringSliceFlag{
					Name:        "tag",
					Aliases:     []string{"t"},
					Sources:     cli.EnvVars("EARTHLY_DOCKER_TAGS"),
					Usage:       "Name and tag for the built image; formatted as 'name:tag'",
					Destination: &b.dockerTags,
				},
				&cli.StringFlag{
					Name:        "target",
					Sources:     cli.EnvVars("EARTHLY_DOCKER_TARGET"),
					Usage:       "The docker target to build in the specified dockerfile",
					Destination: &b.dockerTarget,
				},
			),
		},
	}
}

// Action handles the "build" command.
func (b *Build) Action(ctx context.Context, cmd *cli.Command) error {
	b.cli.SetCommandName("build")

	if b.cli.Flags().CI {
		b.cli.Flags().NoOutput = !b.cli.Flags().Output && !b.cli.Flags().ArtifactMode && !b.cli.Flags().ImageMode
		b.cli.Flags().Strict = true

		if b.cli.Flags().InteractiveDebugging {
			return params.Errorf("unable to use --ci flag in combination with --interactive flag")
		}
	}

	if b.cli.Flags().ImageMode && b.cli.Flags().ArtifactMode {
		return params.Errorf("both image and artifact modes cannot be active at the same time")
	}

	if (b.cli.Flags().ImageMode && b.cli.Flags().NoOutput) || (b.cli.Flags().ArtifactMode && b.cli.Flags().NoOutput) {
		if b.cli.Flags().CI {
			b.cli.Flags().NoOutput = false
		} else {
			return params.Errorf("cannot use --no-output with image or artifact modes")
		}
	}

	if b.cli.Flags().InteractiveDebugging && !termutil.IsTTY() {
		return params.Errorf("A tty-terminal must be present in order to use the --interactive flag")
	}

	flagArgs, nonFlagArgs, err := variables.ParseFlagArgsWithNonFlags(cmd.Args().Slice())
	if err != nil {
		return errors.Wrapf(err, "parse args %s", strings.Join(cmd.Args().Slice(), " "))
	}

	return b.ActionBuildImp(ctx, cmd, flagArgs, nonFlagArgs)
}

// warnIfArgContainsBuildArg will issue a warning if a flag is incorrectly prefixed with build-arg.
// TODO this check should be replaced with a warning if an arg was given but never used.
func (b *Build) warnIfArgContainsBuildArg(flagArgs []string) {
	for _, flag := range flagArgs {
		if strings.HasPrefix(flag, "build-arg=") || strings.HasPrefix(flag, "buildarg=") {
			b.cli.Console().Warnf("Found a flag named %q; flags after the build target should be specified as --KEY=VAL\n", flag)
		}
	}
}

func (b *Build) gitLogLevel() buildkitgitutil.GitLogLevel {
	if b.cli.Flags().Debug {
		return buildkitgitutil.GitLogLevelTrace
	}

	if b.cli.Flags().Verbose {
		return buildkitgitutil.GitLogLevelDebug
	}

	return buildkitgitutil.GitLogLevelDefault
}

func (b *Build) parseTarget(cmd *cli.Command, nonFlagArgs []string) (domain.Target, domain.Artifact, string, error) {
	var (
		target   domain.Target
		artifact domain.Artifact
		destPath = "./"
	)

	switch {
	case b.cli.Flags().ImageMode:
		if len(nonFlagArgs) == 0 {
			_ = cli.ShowAppHelp(cmd)

			return target, artifact, "", params.Errorf(
				"no image reference provided. Try %s --image +<target-name>", common.GetBinaryName())
		} else if len(nonFlagArgs) != 1 {
			_ = cli.ShowAppHelp(cmd)
			return target, artifact, "", params.Errorf("invalid arguments %s", strings.Join(nonFlagArgs, " "))
		}

		targetName := nonFlagArgs[0]

		var err error

		target, err = domain.ParseTarget(targetName)
		if err != nil {
			return target, artifact, "", params.Wrapf(err, "invalid target name %s", targetName)
		}
	case b.cli.Flags().ArtifactMode:
		if len(nonFlagArgs) == 0 {
			_ = cli.ShowAppHelp(cmd)

			return target, artifact, "", params.Errorf(
				"no artifact reference provided. Try %s --artifact +<target-name>/<artifact-name>", common.GetBinaryName())
		} else if len(nonFlagArgs) > 2 {
			_ = cli.ShowAppHelp(cmd)
			return target, artifact, "", params.Errorf("invalid arguments %s", strings.Join(nonFlagArgs, " "))
		}

		artifactName := nonFlagArgs[0]
		if len(nonFlagArgs) == 2 {
			destPath = nonFlagArgs[1]
		}

		var err error

		artifact, err = domain.ParseArtifact(artifactName)
		if err != nil {
			return target, artifact, "", params.Wrapf(err, "invalid artifact name %s", artifactName)
		}

		target = artifact.Target
	default:
		if len(nonFlagArgs) == 0 {
			_ = cli.ShowAppHelp(cmd)

			return target, artifact, "", params.Errorf(
				"no target reference provided. Try %s +<target-name>", common.GetBinaryName())
		} else if len(nonFlagArgs) != 1 {
			_ = cli.ShowAppHelp(cmd)
			return target, artifact, "", params.Errorf("invalid arguments %s", strings.Join(nonFlagArgs, " "))
		}

		targetName := nonFlagArgs[0]

		var err error

		target, err = domain.ParseTarget(targetName)
		if err != nil {
			return target, artifact, "", params.Errorf("invalid target %s", targetName)
		}
	}

	return target, artifact, destPath, nil
}

// ActionBuildImp handles the "build" command implementation.
func (b *Build) ActionBuildImp(ctx context.Context, cmd *cli.Command, flagArgs, nonFlagArgs []string) error {
	target, artifact, destPath, err := b.parseTarget(cmd, nonFlagArgs)
	if err != nil {
		return err
	}

	cleanCollection := cleanup.NewCollection()
	defer cleanCollection.Close()

	b.cli.Console().PrintPhaseHeader(builder.PhaseInit, false, "")
	b.warnIfArgContainsBuildArg(flagArgs)

	showUnexpectedEnvWarnings := true

	dotEnvMap, err := godotenv.Read(b.cli.Flags().EnvFile)
	if err != nil {
		// ignore ErrNotExist when using default .env file
		if cmd.IsSet(flag.EnvFileFlag) || !errors.Is(err, os.ErrNotExist) {
			return errors.Wrapf(err, "read %s", b.cli.Flags().EnvFile)
		}
	}

	argMap, err := godotenv.Read(b.cli.Flags().ArgFile)
	if err == nil {
		showUnexpectedEnvWarnings = false
	} else if cmd.IsSet(flag.ArgFileFlag) || !errors.Is(err, os.ErrNotExist) {
		// ignore ErrNotExist when using default .env file
		return errors.Wrapf(err, "read %s", b.cli.Flags().ArgFile)
	}

	secretsFileMap, err := godotenv.Read(b.cli.Flags().SecretFile)
	if err == nil {
		showUnexpectedEnvWarnings = false
	} else if cmd.IsSet(flag.SecretFileFlag) || !errors.Is(err, os.ErrNotExist) {
		// ignore ErrNotExist when using default .env file
		return errors.Wrapf(err, "read %s", b.cli.Flags().SecretFile)
	}

	if showUnexpectedEnvWarnings {
		validEnvNames := cliutil.GetValidEnvNames(b.cli.App())
		for k := range dotEnvMap {
			if _, found := validEnvNames[k]; !found {
				b.cli.Console().Warnf("unexpected env \"%s\": as of v0.7.0, "+
					"--build-arg values must be defined in .arg (and --secret values in .secret)", k)
			}
		}
	}

	secretsMap, err := common.
		ProcessSecrets(b.secrets, b.secretFiles, secretsFileMap, b.cli.Flags().SecretFile)
	if err != nil {
		return err
	}

	for secretKey := range secretsMap {
		if !ast.IsValidEnvVarName(secretKey) {
			// TODO If the year is 2024 or later, please move this check into processSecrets, and turn it into an error;
			// see https://github.com/earthly/earthly/issues/2883
			b.cli.Console().Warnf(
				"Deprecation: secret key %q does not follow the recommended naming convention "+
					"(a letter followed by alphanumeric characters or underscores); "+
					"this will become an error in a future version of earthly.", secretKey)
		}
	}

	overridingVars, err := common.CombineVariables(argMap, flagArgs, b.buildArgs)
	if err != nil {
		return err
	}

	skipDB, err := bk.NewBuildkitSkipper(b.cli.Flags().LocalSkipDB)
	if err != nil {
		b.cli.Console().WithPrefix(autoSkipPrefix).Warnf("Failed to initialize auto-skip database: %v", err)
	}

	addHashFn, doSkip, err := b.initAutoSkip(ctx, skipDB, target, overridingVars)
	if err != nil {
		b.cli.Console().PrintFailure("auto-skip")
		return err
	}

	if doSkip {
		return nil
	}

	err = b.cli.InitFrontend(ctx, cmd)
	if err != nil {
		return errors.Wrapf(err, "could not init frontend")
	}

	// After configuring frontend, buildkit address should not be empty.
	// It should be set to a local container or remote address at this point.
	if b.cli.Flags().BuildkitdSettings.BuildkitAddress == "" {
		return errors.New("could not determine buildkit address - is Docker or Podman running?")
	}

	bkClient, err := buildkitd.NewClient(
		ctx,
		b.cli.Console(),
		b.cli.Flags().BuildkitdImage,
		b.cli.Flags().ContainerName,
		b.cli.Flags().InstallationName,
		b.cli.Flags().ContainerFrontend,
		b.cli.Version(),
		b.cli.Flags().BuildkitdSettings,
	)
	if err != nil {
		return errors.Wrap(err, "build new buildkitd client")
	}
	defer bkClient.Close()

	platr, err := b.platformResolver(ctx, bkClient, target)
	if err != nil {
		return err
	}

	runnerName, isLocal, err := b.runnerName(ctx)
	if err != nil {
		return err
	}

	localhostProvider, err := localhostprovider.NewLocalhostProvider()
	if err != nil {
		return errors.Wrap(err, "failed to create localhostprovider")
	}

	cacheLocalDir, err := os.MkdirTemp("", "earthly-cache")
	if err != nil {
		return errors.Wrap(err, "make temp dir for cache")
	}
	defer os.RemoveAll(cacheLocalDir)

	defaultLocalDirs := make(map[string]string)
	defaultLocalDirs["earthly-cache"] = cacheLocalDir
	buildContextProvider := provider.NewBuildContextProvider(b.cli.Console())
	buildContextProvider.AddDirs(defaultLocalDirs)

	internalSecretStore := secretprovider.NewMutableMapStore(nil)

	customSecretProviderCmd, err := secretprovider.NewSecretProviderCmd(b.cli.Cfg().Global.SecretProvider)
	if err != nil {
		return errors.Wrap(err, "NewSecretProviderCmd")
	}

	secretProvider := secretprovider.New(
		internalSecretStore,
		secretprovider.NewAWSCredentialProvider(),
		secretprovider.NewMapStore(secretsMap),
		customSecretProviderCmd,
	)

	attachables := []session.Attachable{
		secretProvider,
		buildContextProvider,
		localhostProvider,
	}

	cfg := config.LoadDefaultConfigFile(os.Stderr)

	var attachable session.Attachable

	switch b.cli.Flags().ContainerFrontend.Config().Setting {
	case containerutil.FrontendPodman, containerutil.FrontendPodmanShell:
		attachable = authprovider.NewPodman(ctx, os.Stderr)
	default:
		// includes containerutil.FrontendDocker, containerutil.FrontendDockerShell:
		attachable = dockerauthprovider.NewDockerAuthProvider(cfg, nil)
	}

	authSvr, ok := attachable.(auth.AuthServer)
	if !ok {
		return fmt.Errorf("want auth.AuthServer, got %T", attachable)
	}

	authProvider := authprovider.New(b.cli.Console(), []authprovider.Child{authSvr})
	attachables = append(attachables, authProvider)

	gitLookup := buildcontext.NewGitLookup(b.cli.Console(), b.cli.Flags().SSHAuthSock)

	err = b.updateGitLookupConfig(gitLookup)
	if err != nil {
		return err
	}

	if b.cli.Flags().SSHAuthSock != "" {
		var ssh session.Attachable

		ssh, err = sshprovider.NewSSHAgentProvider([]sshprovider.AgentConfig{{
			Paths: []string{b.cli.Flags().SSHAuthSock},
		}})
		if err != nil {
			return errors.Wrap(err, "ssh agent provider")
		}

		attachables = append(attachables, ssh)
	}

	localArtifactWhiteList := gatewaycrafter.NewLocalArtifactWhiteList()

	socketProvider, err := socketprovider.NewSocketProvider(map[string]socketprovider.SocketAcceptCb{
		"earthly_save_file": getTryCatchSaveFileHandler(localArtifactWhiteList),
		"earthly_interactive": func(ctx context.Context, conn io.ReadWriteCloser) error {
			if !termutil.IsTTY() {
				return errors.New("interactive mode unavailable due to terminal not being tty")
			}

			debugTermConsole := b.cli.Console().WithPrefix("internal-term")

			termErr := terminal.ConnectTerm(ctx, conn, debugTermConsole)
			if termErr != nil {
				return errors.Wrap(termErr, "interactive terminal")
			}

			return nil
		},
	})
	if err != nil {
		return errors.Wrap(err, "ssh agent provider")
	}

	attachables = append(attachables, socketProvider)

	var enttlmnts []entitlements.Entitlement
	if b.cli.Flags().AllowPrivileged {
		enttlmnts = append(enttlmnts, entitlements.EntitlementSecurityInsecure)
	}

	imageResolveMode := llb.ResolveModePreferLocal
	if b.cli.Flags().Pull {
		imageResolveMode = llb.ResolveModeForcePull
	}

	cacheImports := make([]string, 0)

	var cacheImportImageName string
	if b.cli.Flags().RemoteCache != "" {
		cacheImportImageName, _, err = flagutil.ParseImageNameAndAttrs(b.cli.Flags().RemoteCache)
		if err != nil {
			return errors.Wrapf(err, "parse import cache error: %s", b.cli.Flags().RemoteCache)
		}

		cacheImports = append(cacheImports, cacheImportImageName)
	}

	if len(b.cacheFrom) > 0 {
		cacheImports = append(cacheImports, b.cacheFrom...)
	}

	var (
		cacheExport    string
		maxCacheExport string
	)

	if b.cli.Flags().RemoteCache != "" && b.cli.Flags().Push {
		if b.cli.Flags().MaxRemoteCache {
			maxCacheExport = b.cli.Flags().RemoteCache
		} else {
			cacheExport = b.cli.Flags().RemoteCache
		}
	}

	if b.cli.Cfg().Global.ConversionParallelism <= 0 {
		return errors.New("configuration error: \"conversion_parallelism\" must be larger than zero")
	}

	parallelism := semutil.NewWeighted(int64(b.cli.Cfg().Global.ConversionParallelism))

	localRegistryAddr := ""

	if isLocal && b.cli.Flags().LocalRegistryHost != "" {
		var u *url.URL

		u, err = url.Parse(b.cli.Flags().LocalRegistryHost)
		if err != nil {
			return errors.Wrapf(err, "parse local registry host %s", b.cli.Flags().LocalRegistryHost)
		}

		localRegistryAddr = u.Host
	}

	logbusSM := b.cli.LogbusSetup().SolverMonitor

	var vertexStateStore buildkitskipper.VertexStateStore
	if skipDB != nil {
		vertexStateStore = skipDB.VertexStateStore()
		logbusSM.Configure(ctx, vertexStateStore, target.StringCanonical())
	}

	builderOpts := builder.Opt{
		BkClient:                              bkClient,
		LogBusSolverMonitor:                   logbusSM,
		Console:                               b.cli.Console(),
		Verbose:                               b.cli.Flags().Verbose,
		Attachables:                           attachables,
		Enttlmnts:                             enttlmnts,
		NoCache:                               b.cli.Flags().NoCache,
		CacheImports:                          states.NewCacheImports(cacheImports),
		CacheExport:                           cacheExport,
		MaxCacheExport:                        maxCacheExport,
		UseInlineCache:                        b.cli.Flags().UseInlineCache,
		SaveInlineCache:                       b.cli.Flags().SaveInlineCache,
		ImageResolveMode:                      imageResolveMode,
		CleanCollection:                       cleanCollection,
		OverridingVars:                        overridingVars,
		BuildContextProvider:                  buildContextProvider,
		GitLookup:                             gitLookup,
		GitBranchOverride:                     b.cli.Flags().GitBranchOverride,
		UseFakeDep:                            !b.cli.Flags().NoFakeDep,
		Strict:                                b.cli.Flags().Strict,
		DisableNoOutputUpdates:                b.cli.Flags().InteractiveDebugging,
		ParallelConversion:                    (b.cli.Cfg().Global.ConversionParallelism != 0),
		Parallelism:                           parallelism,
		LocalRegistryAddr:                     localRegistryAddr,
		DarwinProxyImage:                      b.cli.Cfg().Global.DarwinProxyImage,
		DarwinProxyWait:                       b.cli.Cfg().Global.DarwinProxyWait,
		FeatureFlagOverrides:                  b.cli.Flags().FeatureFlagOverrides,
		ContainerFrontend:                     b.cli.Flags().ContainerFrontend,
		InternalSecretStore:                   internalSecretStore,
		InteractiveDebugging:                  b.cli.Flags().InteractiveDebugging,
		InteractiveDebuggingDebugLevelLogging: b.cli.Flags().Debug,
		GitImage:                              b.cli.Cfg().Global.GitImage,
		GitLFSInclude:                         b.cli.Flags().GitLFSPullInclude,
		GitLogLevel:                           b.gitLogLevel(),
		DisableRemoteRegistryProxy:            b.cli.Flags().DisableRemoteRegistryProxy,
		BuildkitSkipper:                       skipDB,
		VertexStateStore:                      vertexStateStore,
		NoAutoSkip:                            b.cli.Flags().NoAutoSkip,
	}

	build, err := builder.NewBuilder(builderOpts)
	if err != nil {
		return errors.Wrap(err, "new builder")
	}

	b.cli.Console().PrintPhaseFooter(builder.PhaseInit)

	builtinArgs := variables.DefaultArgs{
		EarthVersion:  b.cli.Version(),
		EarthBuildSha: b.cli.GitSHA(),
	}

	buildOpts := builder.BuildOpt{
		PrintPhases:                true,
		Push:                       b.cli.Flags().Push,
		CI:                         b.cli.Flags().CI,
		NoOutput:                   b.cli.Flags().NoOutput,
		OnlyFinalTargetImages:      b.cli.Flags().ImageMode,
		PlatformResolver:           platr,
		EnableGatewayClientLogging: b.cli.Flags().Debug,
		BuiltinArgs:                builtinArgs,
		LocalArtifactWhiteList:     localArtifactWhiteList,
		Logbus:                     b.cli.Logbus(),
		Runner:                     runnerName,

		// feature-flip the removal of builder.go code
		// once VERSION 0.7 is released AND support for 0.6 is dropped,
		// we can remove this flag along with code from builder.go.
		GlobalWaitBlockFtr: b.cli.Flags().GlobalWaitEnd,

		// explicitly set this to true at the top level (without granting the entitlements.EntitlementSecurityInsecure
		// buildkit option), to differentiate between a user forgetting to run "earth -P", versus a remotely referencing
		// an earthfile that requires privileged.
		AllowPrivileged: true,

		ProjectAdder: authProvider,
	}
	if b.cli.Flags().ArtifactMode {
		buildOpts.OnlyArtifact = &artifact
		buildOpts.OnlyArtifactDestPath = destPath
	}

	_, err = build.BuildTarget(ctx, target, buildOpts)
	if err != nil {
		return errors.Wrap(err, "build target")
	}

	if b.cli.Flags().SkipBuildkit && addHashFn != nil {
		addHashFn()
	}

	return nil
}

// getTryCatchSaveFileHandler implements [socketprovider.SocketAcceptCb] -
// returns a handler function for the earthly_save_file socket.
func getTryCatchSaveFileHandler(
	localArtifactWhiteList *gatewaycrafter.LocalArtifactWhiteList,
) func(ctx context.Context, conn io.ReadWriteCloser) error {
	return func(_ context.Context, conn io.ReadWriteCloser) error {
		// version
		protocolVersion, _, err := debuggercommon.ReadDataPacket(conn)
		if err != nil {
			return err
		}

		switch protocolVersion {
		case 1:
			return receiveFileVersion1(conn, localArtifactWhiteList)
		case 2:
			return receiveFileVersion2(conn, localArtifactWhiteList)
		default:
			return fmt.Errorf("unexpected version %d", protocolVersion)
		}
	}
}

func (b *Build) updateGitLookupConfig(gitLookup *buildcontext.GitLookup) error {
	for k, v := range b.cli.Cfg().Git {
		if k == "github" || k == "gitlab" || k == "bitbucket" {
			b.cli.Console().Warnf("git configuration for %q found, did you mean %q?\n", k, k+".com")
		}

		pattern := v.Pattern
		if pattern == "" {
			// if empty, assume it will be of the form host.com/user/repo.git
			host := k
			if !strings.Contains(host, ".") {
				host += ".com"
			}

			pattern = regexp.QuoteMeta(host) + "/[^/]+/[^/]+"
		}

		auth := v.Auth

		suffix := v.Suffix
		if suffix == "" {
			suffix = ".git"
		}

		err := gitLookup.AddMatcher(
			k, pattern, v.Substitute, v.User, v.Password, v.Prefix, suffix, auth, v.ServerKey,
			common.IfNilBoolDefault(v.StrictHostKeyChecking, true), v.Port, v.SSHCommand)
		if err != nil {
			return errors.Wrap(err, "gitlookup")
		}
	}

	return nil
}

func receiveFileVersion1(conn io.ReadWriteCloser, localArtifactWhiteList *gatewaycrafter.LocalArtifactWhiteList) error {
	// dst path
	_, dst, err := debuggercommon.ReadDataPacket(conn)
	if err != nil {
		return err
	}

	if !localArtifactWhiteList.Exists(string(dst)) {
		return fmt.Errorf("file %s does not appear in the white list", dst)
	}

	// data
	_, data, err := debuggercommon.ReadDataPacket(conn)
	if err != nil {
		return err
	}

	// EOF
	n, _, err := debuggercommon.ReadDataPacket(conn)
	if err != nil {
		return err
	}

	if n != 0 {
		return errors.New("expected EOF, but got more data")
	}

	f, err := os.Create(string(dst))
	if err != nil {
		return err
	}

	_, err = f.Write(data)
	if err != nil {
		return err
	}

	return f.Close()
}

func receiveFileVersion2(
	conn io.ReadWriteCloser, localArtifactWhiteList *gatewaycrafter.LocalArtifactWhiteList,
) (retErr error) {
	// dst path
	dst, err := debuggercommon.ReadUint16PrefixedData(conn)
	if err != nil {
		return err
	}

	if !localArtifactWhiteList.Exists(string(dst)) {
		return fmt.Errorf("file %s does not appear in the white list", dst)
	}

	err = os.MkdirAll(path.Dir(string(dst)), 0o755) // #nosec G301
	if err != nil {
		return err
	}

	f, err := os.Create(string(dst))
	if err != nil {
		return err
	}

	defer func() {
		if retErr != nil {
			// don't output incomplete data
			_ = f.Close()
			_ = os.Remove(string(dst))
		}
	}()

	// data
	for {
		data, err := debuggercommon.ReadUint16PrefixedData(conn)
		if err != nil {
			return err
		}

		if len(data) == 0 {
			break
		}

		_, err = f.Write(data)
		if err != nil {
			return err
		}
	}

	return f.Close()
}

// runnerName returns the name of the local or remote BK "runner"; which is a
// representation of what BuildKit instance is being used,
// e.g. local:<hostname>, sat:<org>/<name>, or bk:<remote-address>.
func (b *Build) runnerName(ctx context.Context) (string, bool, error) {
	var runnerName string

	isLocal := containerutil.IsLocal(b.cli.Flags().BuildkitdSettings.BuildkitAddress)
	if isLocal {
		hostname, err := os.Hostname()
		if err != nil {
			b.cli.Console().Warnf("failed to get hostname: %v", err)

			hostname = "unknown"
		}

		runnerName = "local:" + hostname
	} else {
		runnerName = "bk:" + b.cli.Flags().BuildkitdSettings.BuildkitAddress
	}

	if !isLocal && (b.cli.Flags().UseInlineCache || b.cli.Flags().SaveInlineCache) {
		b.cli.Console().Warnf("Note that inline cache (--use-inline-cache and --save-inline-cache) occasionally cause " +
			"builds to get stuck at 100%% CPU on remote Buildkit.")
		b.cli.Console().Warnf("")
	}

	if isLocal && !b.cli.Flags().ContainerFrontend.IsAvailable(ctx) {
		return "", false, errors.New("Frontend is not available to perform the build. Is Docker installed and running?")
	}

	return runnerName, isLocal, nil
}

func (b *Build) platformResolver(
	ctx context.Context, bkClient *bkclient.Client, target domain.Target,
) (*platutil.Resolver, error) {
	nativePlatform, err := platutil.GetNativePlatformViaBkClient(ctx, bkClient)
	if err != nil {
		return nil, errors.Wrap(err, "get native platform via buildkit client")
	}

	b.cli.LogbusSetup().SetDefaultPlatform(platforms.Format(nativePlatform))
	platr := platutil.NewResolver(nativePlatform)
	platr.AllowNativeAndUser = true

	platformsSlice := make([]platutil.Platform, 0, len(b.platformsStr))
	for _, p := range b.platformsStr {
		platform, err := platr.Parse(p)
		if err != nil {
			return nil, errors.Wrapf(err, "parse platform %s", p)
		}

		platformsSlice = append(platformsSlice, platform)
	}

	switch len(platformsSlice) {
	case 0:
	case 1:
		platr.UpdatePlatform(platformsSlice[0])
	default:
		return nil, errors.Errorf("multi-platform builds are not yet supported on the command line. "+
			"You may, however, create a target with the instruction BUILD --platform ... --platform ... %s", target)
	}

	return platr, nil
}

func (b *Build) initAutoSkip(
	ctx context.Context, skipDB bk.BuildkitSkipper, target domain.Target, overridingVars *variables.Scope,
) (func(), bool, error) {
	if !b.cli.Flags().SkipBuildkit {
		return nil, false, nil
	}

	console := b.cli.Console().WithPrefix(autoSkipPrefix)

	if skipDB == nil {
		return nil, false, nil
	}

	consoleNoPrefix := b.cli.Console()

	if b.cli.Flags().NoCache {
		return nil, false, errors.New("--no-cache cannot be used with --auto-skip")
	}

	if b.cli.Flags().NoAutoSkip {
		return nil, false, errors.New("--no-auto-skip cannot be used with --auto-skip")
	}

	targetHash, stats, err := inputgraph.HashTarget(ctx, inputgraph.HashOpt{
		Target:         target,
		Console:        b.cli.Console(),
		CI:             b.cli.Flags().CI,
		BuiltinArgs:    variables.DefaultArgs{EarthVersion: b.cli.Version(), EarthBuildSha: b.cli.GitSHA()},
		OverridingVars: overridingVars,
	})
	if err != nil {
		return nil, false, errors.Wrapf(err, "auto-skip is unable to calculate hash for %s", target)
	}

	console.VerbosePrintf("targets visited: %d; targets hashed: %d; target cache hits: %d",
		stats.TargetsVisited, stats.TargetsHashed, stats.TargetCacheHits)
	console.VerbosePrintf("hash calculation took %s", stats.Duration)

	if !target.IsRemote() {
		var meta *gitutil.GitMetadata

		meta, err = gitutil.Metadata(ctx, target.GetLocalPath(), b.cli.Flags().GitBranchOverride)
		if err != nil {
			console.VerboseWarnf("unable to detect all git metadata: %v", err.Error())
		}

		var ok bool

		target, ok = gitutil.ReferenceWithGitMeta(target, meta).(domain.Target)
		if !ok {
			return nil, false, errors.Errorf("want domain.Target, got %T", target)
		}

		target.Tag = ""
	}

	targetConsole := b.cli.Console().WithPrefix(target.String())
	targetStr := targetConsole.PrefixColor().Sprint(target.StringCanonical())

	exists, err := skipDB.Exists(ctx, targetHash)
	if err != nil {
		console.Warnf("Unable to check if target %s (hash %x) has already been run: %s", targetStr, targetHash, err.Error())
		return nil, false, nil
	}

	if exists {
		console.Printf("Target %s (hash %x) has already been run. Skipping.", targetStr, targetHash)
		consoleNoPrefix.PrintSuccess()

		return nil, true, nil
	}

	// Cache miss: log the inputs that were hashed so the user can understand
	// what will cause this target to be rebuilt.
	if len(stats.HashLog) > 0 {
		console.VerbosePrintf("cache miss for %s — hashed inputs:", targetStr)

		for _, entry := range stats.HashLog {
			console.VerbosePrintf("  %-16s %s", entry.Label, entry.Detail)
		}
	}

	addHashFn := func() {
		err := skipDB.Add(ctx, target.StringCanonical(), targetHash)
		if err != nil {
			b.cli.Console().WithPrefix(autoSkipPrefix).
				Warnf("failed to record %s (hash %x) as completed: %s", target.String(), target, err)
		}
	}

	return addHashFn, false, nil
}

func (b *Build) actionDockerBuild(ctx context.Context, cmd *cli.Command) error {
	b.cli.SetCommandName("docker-build")

	flagArgs, nonFlagArgs, err := variables.ParseFlagArgsWithNonFlags(cmd.Args().Slice())
	if err != nil {
		return errors.Wrapf(err, "parse args %s", strings.Join(cmd.Args().Slice(), " "))
	}

	if len(nonFlagArgs) == 0 {
		_ = cli.ShowAppHelp(cmd)

		return errors.Errorf(
			"no build context path provided. Try %s docker-build <path>", common.GetBinaryName())
	}

	if len(nonFlagArgs) != 1 {
		_ = cli.ShowAppHelp(cmd)
		return errors.Errorf("invalid arguments %s", strings.Join(nonFlagArgs, " "))
	}

	buildContextPath, err := filepath.Abs(nonFlagArgs[0])
	if err != nil {
		return errors.Wrapf(err, "failed to get absolute path for build context")
	}

	tempDir, err := os.MkdirTemp("", "docker-build")
	if err != nil {
		return errors.Wrap(err, "docker-build: failed to create temporary dir for Earthfile")
	}
	defer os.RemoveAll(tempDir)

	argMap, err := godotenv.Read(b.cli.Flags().ArgFile)
	if err != nil && (cmd.IsSet(flag.ArgFileFlag) || !errors.Is(err, os.ErrNotExist)) {
		return errors.Wrapf(err, "read %q", b.cli.Flags().ArgFile)
	}

	buildArgs, err := common.CombineVariables(argMap, flagArgs, b.buildArgs)
	if err != nil {
		return errors.Wrapf(err, "combining build args")
	}

	platforms := flagutil.SplitFlagString(b.platformsStr)

	content, err := docker2earth.GenerateEarthfile(
		buildContextPath, b.cli.Flags().DockerfilePath, b.dockerTags,
		buildArgs.Sorted(), platforms, b.dockerTarget)
	if err != nil {
		return errors.Wrap(err, "docker-build: failed to wrap Dockerfile with an Earthfile")
	}

	earthfilePath := filepath.Join(tempDir, buildcontext.Earthfile)

	out, err := os.Create(earthfilePath) // #nosec G304
	if err != nil {
		return errors.Wrapf(err, "docker-build: failed to create Earthfile %q", earthfilePath)
	}
	defer out.Close()

	_, err = out.WriteString(content)
	if err != nil {
		return errors.Wrapf(err, "docker-build: failed to write to %q", earthfilePath)
	}

	// The following should not be set in the context of executing the build from the generated Earthfile:
	b.cli.Flags().DockerfilePath = ""
	b.cli.Flags().ImageMode = false
	b.cli.Flags().ArtifactMode = false
	b.dockerTarget = ""
	b.dockerTags = []string{}
	b.platformsStr = []string{}

	nonFlagArgs = []string{tempDir + "+build"}

	return b.ActionBuildImp(ctx, cmd, flagArgs, nonFlagArgs)
}
