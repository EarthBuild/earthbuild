package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	_ "net/http/pprof" // #nosec G108 // enable pprof handlers on net/http listener
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/EarthBuild/earthbuild/internal/observe"
	"github.com/EarthBuild/earthbuild/internal/version"
	"github.com/fatih/color"
	"github.com/joho/godotenv"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // Load "docker-container://" helper.
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"

	"github.com/EarthBuild/earthbuild/cmd/earthly/app"
	"github.com/EarthBuild/earthbuild/cmd/earthly/base"
	"github.com/EarthBuild/earthbuild/cmd/earthly/common"
	eFlag "github.com/EarthBuild/earthbuild/cmd/earthly/flag"
	"github.com/EarthBuild/earthbuild/cmd/earthly/subcmd"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/util/envutil"
	"github.com/EarthBuild/earthbuild/util/syncutil"
)

// These vars are set by ldflags.
var (
	// Version is the version of this CLI app.
	Version string
	// GitSha contains the git sha used to build this app.
	GitSha string
	// BuiltBy contains information on which build-system was used (e.g. official earthly binaries, homebrew, etc).
	BuiltBy string

	// DefaultBuildkitdImage is the default buildkitd image to use.
	DefaultBuildkitdImage string

	// DefaultInstallationName is the name included in the various earthly global resources on the system,
	// such as the ~/.earthly dir name, the buildkitd container name, the docker volume name, etc.
	// This should be set to "earthly" for official releases.
	DefaultInstallationName string
)

func setExportableVars() {
	version.Version = Version
	version.GitSha = GitSha
	version.BuiltBy = BuiltBy
}

func main() {
	os.Exit(run())
}

// run executes the CLI and returns an exit code to pass to [os.Exit].
func run() (code int) {
	ctx := context.Background()

	shutdown, err := observe.Setup(ctx)
	if err != nil {
		fmt.Printf("Error setting up OpenTelemetry: %s\n", err.Error())
	} else {
		defer shutdown(ctx)
	}

	ctx, span := otel.Tracer("earth").Start(ctx, "main")
	defer span.End()

	defer func() {
		span.SetAttributes(semconv.ProcessExitCode(code))
	}()

	setExportableVars()

	startTime := time.Now()
	ctx, cancel := context.WithCancel(ctx)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	defer func() {
		signal.Stop(sigChan)
		cancel()
	}()

	lastSignal := &syncutil.Signal{}

	go func() {
		for sig := range sigChan {
			if lastSignal.Get() != nil {
				// This is the second time we have received a signal. Quit immediately.
				fmt.Printf("Received second signal %s. Forcing exit.\n", sig.String())

				code = 9

				return
			}

			lastSignal.Set(sig)
			cancel()
			fmt.Printf("Received signal %s. Cleaning up before exiting...\n", sig.String())

			go func() {
				// Wait for 30 seconds before forcing an exit.
				time.Sleep(30 * time.Second)
				fmt.Printf("Timed out cleaning up. Forcing exit.\n")

				code = 9
			}()
		}
	}()
	// Occasional spurious warnings show up - these are coming from imported libraries. Discard them.
	logrus.StandardLogger().Out = io.Discard

	// Load .env into current global env's. This is mainly for applying Earthly settings.
	// Separate call is made for build args and secrets.
	envFile := eFlag.DefaultEnvFile
	envFileOverride := false

	if envFileFromEnv, ok := os.LookupEnv("EARTHLY_ENV_FILE"); ok {
		envFile = envFileFromEnv
		envFileOverride = true
	}

	envFileFromArgOK := true
	flagSet := flag.NewFlagSet(common.GetBinaryName(), flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	cli := base.NewCLI(conslogging.ConsoleLogger{},
		base.WithVersion(Version),
		base.WithGitSHA(GitSha),
		base.WithBuiltBy(BuiltBy),
		base.WithDefaultBuildkitdImage(DefaultBuildkitdImage),
		base.WithDefaultInstallationName(DefaultInstallationName),
	)
	buildApp := subcmd.NewBuild(cli)
	rootApp := subcmd.NewRoot(cli, buildApp)

	for _, f := range cli.Flags().RootFlags(DefaultInstallationName, DefaultBuildkitdImage) {
		err = f.Apply(flagSet)
		if err != nil {
			envFileFromArgOK = false
			break
		}
	}

	if envFileFromArgOK {
		err = flagSet.Parse(os.Args[1:])
		if err == nil {
			if envFileFlag := flagSet.Lookup(eFlag.EnvFileFlag); envFileFlag != nil {
				envFile = envFileFlag.Value.String()
				envFileOverride = envFile != eFlag.DefaultEnvFile // flag lib doesn't expose if a value was set or not
			}
		}
	}

	err = godotenv.Load(envFile)
	if err != nil {
		// ignore ErrNotExist when using default .env file
		if envFileOverride || !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("Error loading dot-env file %s: %s\n", envFile, err.Error())
			return 1
		}
	}

	colorMode := conslogging.AutoColor
	if envutil.IsTrue("FORCE_COLOR") {
		colorMode = conslogging.ForceColor
		color.NoColor = false
	}

	if envutil.IsTrue("NO_COLOR") {
		colorMode = conslogging.NoColor
		color.NoColor = true
	}

	padding := conslogging.DefaultPadding

	customPadding, ok := os.LookupEnv("EARTHLY_TARGET_PADDING")
	if ok {
		targetPadding, err := strconv.Atoi(customPadding)
		if err == nil {
			padding = targetPadding
		}
	}

	if envutil.IsTrue("EARTHLY_FULL_TARGET") {
		padding = conslogging.NoPadding
	}

	logging := conslogging.Current(colorMode, padding, conslogging.Info, cli.Flags().GithubAnnotations)

	cli.SetConsole(logging)
	earthly := app.NewEarthlyApp(cli, rootApp, buildApp, ctx)

	return earthly.Run(ctx, logging, startTime, lastSignal)
}
