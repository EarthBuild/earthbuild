package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"
	"uuid"

	"github.com/EarthBuild/earthbuild/engine/timing"

	"github.com/EarthBuild/earthbuild/buildkitd"
	"github.com/EarthBuild/earthbuild/cmd/earth/subcmd"
	"github.com/EarthBuild/earthbuild/config"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/env"
	logbussetup "github.com/EarthBuild/earthbuild/logbus/setup"
	"github.com/EarthBuild/earthbuild/util/cliutil"
	"github.com/EarthBuild/earthbuild/util/containerutil"
	"github.com/EarthBuild/earthbuild/util/execstatssummary"
	"github.com/EarthBuild/earthbuild/util/fileutil"
	"github.com/urfave/cli/v3"
)

func (app *EarthApp) before(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	flags := app.BaseCLI.Flags()

	if flags.EnableProfiler {
		go profhandler()
	}

	if flags.InstallationName != "" {
		if !cmd.IsSet("config") {
			flags.ConfigPath = defaultConfigPath(flags.InstallationName)
		}

		if !cmd.IsSet("buildkit-container-name") {
			flags.ContainerName = buildkitd.ContainerName(flags.InstallationName)
		}

		if !cmd.IsSet("buildkit-volume-name") {
			flags.BuildkitdSettings.VolumeName = buildkitd.VolumeName(flags.InstallationName)
		}
	}

	if flags.Debug {
		app.BaseCLI.SetLog(app.BaseCLI.Log().WithLogLevel(conslogging.Debug))
	} else if flags.Verbose {
		app.BaseCLI.SetLog(app.BaseCLI.Log().WithLogLevel(conslogging.Verbose))
	}

	app.BaseCLI.SetLog(app.BaseCLI.Log().WithPrefixWriter(app.BaseCLI.Logbus().Run().Generic()))

	var execStatsTracker *execstatssummary.Tracker
	if flags.ExecStatsSummary != "" {
		execStatsTracker = execstatssummary.NewTracker(flags.ExecStatsSummary)
	}

	busSetup, err := logbussetup.New(
		ctx,
		app.BaseCLI.Logbus(),
		flags.Debug,
		flags.Verbose,
		flags.DisplayExecStats,
		app.BaseCLI.Flags().InteractiveDebugging,
		flags.LogstreamDebugFile,
		uuid.New().String(),
		execStatsTracker,
		flags.GithubAnnotations,
	)
	if err != nil {
		return ctx, fmt.Errorf("logbus setup: %w", err)
	}

	app.BaseCLI.SetLogbusSetup(busSetup)

	if cmd.IsSet("config") {
		app.BaseCLI.Log().Printf("loading config values from %q\n", flags.ConfigPath)
	}

	var yamlData []byte

	if flags.ConfigPath != "" {
		yamlData, err = config.ReadConfigFile(flags.ConfigPath)
		if err != nil {
			if cmd.IsSet("config") || !errors.Is(err, os.ErrNotExist) {
				return ctx, fmt.Errorf("read config: %w", err)
			}
		}
	}

	cfg, err := config.ParseYAML(yamlData, flags.InstallationName)
	if err != nil {
		return ctx, fmt.Errorf("failed to parse %s: %w", flags.ConfigPath, err)
	}

	app.BaseCLI.SetCfg(&cfg)
	app.processDeprecatedCommandOptions(app.BaseCLI.Cfg())

	// **Skipped outright for a native build**, which cannot use the result: it
	// costs 116ms of a 380ms cached build to run the candidate binaries and ask
	// which of them answers (E871).
	endFrontend := timing.Phase("frontend:detect", "")
	err = app.parseFrontend(ctx, needsContainerFrontend(
		os.Args[1:], commandNames(app.BaseCLI.App().Commands), engineEnv()))
	endFrontend()
	if err != nil {
		return ctx, err
	}

	// Make a small attempt to check if we are not bootstrapped. If not, then do that before we do anything else.
	isBootstrapCmd := false
	for _, f := range cmd.Args().Slice() {
		isBootstrapCmd = f == "bootstrap"

		if isBootstrapCmd {
			break
		}
	}

	if !isBootstrapCmd && !cliutil.IsBootstrapped(flags.InstallationName) {
		// Docker may not be available, for instance... like our integration tests.
		app.BaseCLI.Flags().BootstrapNoBuildkit = true
		newBootstrap := subcmd.NewBootstrap(app.BaseCLI)

		err = newBootstrap.Action(ctx, cmd)
		if err != nil {
			return ctx, fmt.Errorf("bootstrap unbootstrclied installation: %w", err)
		}
	}

	return ctx, nil
}

func (app *EarthApp) parseFrontend(ctx context.Context, detect bool) error {
	log := app.BaseCLI.Log().WithPrefix("frontend")
	feCfg := &containerutil.FrontendConfig{
		BuildkitHostCLIValue:       app.BaseCLI.Flags().BuildkitHost,
		BuildkitHostFileValue:      app.BaseCLI.Cfg().Global.BuildkitHost,
		LocalRegistryHostFileValue: app.BaseCLI.Cfg().Global.LocalRegistryHost,
		LocalContainerName:         app.BaseCLI.Flags().ContainerName,
		DefaultPort:                8372 + config.PortOffset(app.BaseCLI.Flags().InstallationName),
		Log:                        log,
	}

	// The same stub the detection falls back to when no daemon answers, which
	// is the honest description of this build: there is no container frontend,
	// and nothing on this path will ask for one.
	if !detect {
		stub, err := containerutil.NewStubFrontend(feCfg)
		if err != nil {
			return fmt.Errorf("failed stub frontend initialization: %w", err)
		}

		app.BaseCLI.Flags().ContainerFrontend = stub
		log.VerbosePrintf("no container frontend detected: this build does not use one\n")

		return nil
	}

	fe, err := containerutil.FrontendForSetting(ctx, app.BaseCLI.Cfg().Global.ContainerFrontend, feCfg)
	if err != nil {
		origErr := err

		stub, err := containerutil.NewStubFrontend(feCfg)
		if err != nil {
			return fmt.Errorf("failed stub frontend initialization: %w", err)
		}

		app.BaseCLI.Flags().ContainerFrontend = stub

		if !app.BaseCLI.Flags().Verbose {
			log.Printf("Unable to detect Docker or Podman. Use --verbose to see details (or errors)\n")
		}

		log.VerbosePrintf("%s frontend initialization failed due to %s",
			app.BaseCLI.Cfg().Global.ContainerFrontend, origErr.Error())

		return nil
	}

	log.VerbosePrintf("%s frontend initialized.\n", fe.Config().Setting)
	app.BaseCLI.Flags().ContainerFrontend = fe

	// These URLs were calculated relative to the configured frontend. In the
	// case of an automatically detected frontend, they are calculated according
	// to the first selected one in order of precedence.
	buildkitURLs := app.BaseCLI.Flags().ContainerFrontend.Config().FrontendURLs
	app.BaseCLI.Flags().BuildkitHost = buildkitURLs.BuildkitHost.String()
	app.BaseCLI.Flags().LocalRegistryHost = buildkitURLs.LocalRegistryHost.String()

	return nil
}

func (app *EarthApp) processDeprecatedCommandOptions(cfg *config.Config) {
	app.warnRenamedFromEarthly()
	app.warnDeprecatedEarthlyEnvVars()
	app.warnDeprecatedAutoSkip()

	flags := app.BaseCLI.Flags()

	if cfg.Global.CachePath != "" {
		app.BaseCLI.Log().Warnf("Warning: the setting cache_path is now obsolete and will be ignored")
	}

	if flags.ConversionParallelism != 0 {
		app.BaseCLI.Log().Warnf("Warning: --conversion-parallelism and EARTHLY_CONVERSION_PARALLELISM is obsolete, " +
			"please use 'earth config global.conversion_parallelism <parallelism>' instead")
	}

	// command line overrides the config file
	if flags.GitUsernameOverride != "" || flags.GitPasswordOverride != "" {
		app.BaseCLI.Log().Warnf("Warning: the --git-username and --git-password command flags " +
			"are deprecated and are now configured in the ~/.earthly/config.yml file under the git section; " +
			"see https://docs.earthbuild.dev/earthly-config for reference.\n")

		if _, ok := cfg.Git["github.com"]; !ok {
			cfg.Git["github.com"] = config.GitConfig{}
		}

		if _, ok := cfg.Git["gitlab.com"]; !ok {
			cfg.Git["gitlab.com"] = config.GitConfig{}
		}

		for k, v := range cfg.Git {
			v.Auth = "https"
			if flags.GitUsernameOverride != "" {
				v.User = flags.GitUsernameOverride
			}

			if flags.GitPasswordOverride != "" {
				v.Password = flags.GitPasswordOverride
			}

			cfg.Git[k] = v
		}
	}
}

const (
	// cmdName is the current name of the CLI binary.
	cmdName = "earth"
	// deprecatedCmdName is the pre-rename name, still shipped as a symlink to
	// cmdName for one deprecation cycle.
	deprecatedCmdName = "earthly"
)

// warnDeprecatedEarthlyEnvVars warns about any EARTHLY_-prefixed environment
// variables, which have been replaced by the EARTH_ prefix.
//
// NOTE: this is a temporary shim for the EARTHLY_ -> EARTH_ migration and should
// be removed once EARTHLY_ support is officially dropped.
func (app *EarthApp) warnDeprecatedEarthlyEnvVars() {
	for _, warning := range env.DeprecatedWarnings() {
		app.BaseCLI.Log().Warn(warning)
	}
}

// warnDeprecatedAutoSkip warns when any of the auto-skip flags or env vars are
// used. The cloud backend that once powered auto-skip has been removed; only
// the local database (--auto-skip-db-path) still functions. The flags and env
// vars are deprecated, and we are collecting feedback to decide whether to
// remove them in the future.
func (app *EarthApp) warnDeprecatedAutoSkip() {
	flags := app.BaseCLI.Flags()
	if warning := autoSkipDeprecationWarning(flags.SkipBuildkit, flags.NoAutoSkip, flags.LocalSkipDB); warning != "" {
		app.BaseCLI.Log().Warnf("%s", warning)
	}
}

// autoSkipDeprecationWarning returns the auto-skip deprecation warning when any
// auto-skip flag (or its env var) is set, or an empty string otherwise. It is
// the testable core of warnDeprecatedAutoSkip.
func autoSkipDeprecationWarning(skipBuildkit, noAutoSkip bool, localSkipDB string) string {
	if !skipBuildkit && !noAutoSkip && localSkipDB == "" {
		return ""
	}

	return "Deprecation: --auto-skip, --no-auto-skip and --auto-skip-db-path (and their " +
		"EARTH_AUTO_SKIP* / EARTHLY_AUTO_SKIP* env vars) are deprecated. " +
		"The cloud auto-skip backend has been removed; only the local database (--auto-skip-db-path) still functions. " +
		"We may remove these in a future release and are collecting feedback to help decide. " +
		"Let us know how you use auto-skip at https://github.com/orgs/EarthBuild/discussions/707"
}

// warnRenamedFromEarthly warns when the CLI is invoked under its deprecated
// name, and points at the replacement when one is installed alongside it.
func (app *EarthApp) warnRenamedFromEarthly() {
	if len(os.Args) == 0 {
		return
	}

	// Detect the invoked name from argv[0] rather than os.Executable(): the
	// latter resolves the symlink and would always report cmdName.
	if path.Base(os.Args[0]) != deprecatedCmdName {
		return
	}

	app.BaseCLI.Log().Warnf(
		"Warning: the %s binary has been renamed to %s; %s is deprecated and will one day be removed.",
		deprecatedCmdName, cmdName, deprecatedCmdName)

	// Locate the replacement next to the real binary. os.Executable() is the
	// right source here (and argv[0] is not): argv[0] is a bare name when the
	// CLI was found on PATH, which filepath.Abs would resolve against the
	// working directory instead of the install dir.
	exePath, err := os.Executable()
	if err != nil {
		return
	}

	binDir := filepath.Dir(exePath)

	// Only suggest removing the deprecated binary if its replacement actually
	// exists alongside it.
	if exists, _ := fileutil.FileExists(filepath.Join(binDir, cmdName)); !exists {
		return
	}

	deprecatedPath := filepath.Join(binDir, deprecatedCmdName)
	if exists, _ := fileutil.FileExists(deprecatedPath); !exists {
		return
	}

	app.BaseCLI.Log().Warnf("Once you are ready to switch over to %s, you can `rm %s`", cmdName, deprecatedPath)
}

func profhandler() {
	const addr = "127.0.0.1:6060"

	const readHeaderTimeout = 5 * time.Second // arbitrary timeout

	fmt.Printf("listening for pprof on %s\n", addr)
	srv := &http.Server{Addr: addr, ReadHeaderTimeout: readHeaderTimeout}

	err := srv.ListenAndServe()
	if err != nil {
		fmt.Printf("error listening for pprof: %v", err)
	}
}

func defaultConfigPath(installName string) string {
	earthDir := cliutil.GetEarthDir(installName)
	oldConfig := filepath.Join(earthDir, "config.yaml")
	newConfig := filepath.Join(earthDir, "config.yml")
	oldConfigExists, _ := fileutil.FileExists(oldConfig)

	newConfigExists, _ := fileutil.FileExists(newConfig)
	if oldConfigExists && !newConfigExists {
		return oldConfig
	}

	return newConfig
}
