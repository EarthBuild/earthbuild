// Package main is the primary entry point for the earth CLI executable.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	// TODO(jhorsts): this can be removed when earthbuild/buildkit repo is up to date
	// GRPC_ENFORCE_ALPN_ENABLED is set to "false" via the disable_alpn package import
	// to ensure it happens before other packages initialize.
	_ "github.com/EarthBuild/earthbuild/cmd/earthly/disable_alpn"

	"github.com/EarthBuild/earthbuild/cmd/earthly/app"
	"github.com/EarthBuild/earthbuild/cmd/earthly/base"
	"github.com/EarthBuild/earthbuild/cmd/earthly/common"
	eFlag "github.com/EarthBuild/earthbuild/cmd/earthly/flag"
	"github.com/EarthBuild/earthbuild/cmd/earthly/subcmd"
	"github.com/EarthBuild/earthbuild/conslogging"
	"github.com/EarthBuild/earthbuild/internal/env"
	"github.com/EarthBuild/earthbuild/internal/telemetry"
	"github.com/EarthBuild/earthbuild/internal/version"
	"github.com/EarthBuild/earthbuild/util/syncutil"
	"github.com/fatih/color"
	"github.com/joho/godotenv"
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // Load "docker-container://" helper.
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	urfavecli "github.com/urfave/cli/v3"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
)

// These vars are set by ldflags.
var (
	// Version is the version of this CLI app.
	Version string
	// GitSha contains the git sha used to build this app.
	GitSha string
	// BuiltBy contains information on which build-system was used (e.g. official earth binaries, homebrew, etc).
	BuiltBy string

	// DefaultBuildkitdImage is the default buildkitd image to use.
	DefaultBuildkitdImage string

	// DefaultInstallationName is the name included in the various earth global resources on the system,
	// such as the ~/.earthly dir name, the buildkitd container name, the docker volume name, etc.
	// This should be set to "earth" for official releases.
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
	// set up OpenTelemetry
	ctx := context.Background()

	shutdown, err := telemetry.Setup(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up OpenTelemetry: %s\n", err.Error())
	} else {
		defer shutdown(ctx)
	}

	ctx = telemetry.WithTraceparent(ctx)

	ctx, span := telemetry.Tracer().Start(ctx, "main")
	defer span.End()

	defer func() {
		span.SetAttributes(semconv.ProcessExitCode(code))
	}()

	// main

	setExportableVars()

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

	// Load .env into current global env's. This is mainly for applying earth settings.
	// Separate call is made for build args and secrets.
	envFile := eFlag.DefaultEnvFile
	envFileOverride := false

	if envFileFromEnv, ok := env.Lookup("ENV_FILE"); ok {
		envFile = envFileFromEnv
		envFileOverride = true
	}

	envFileFromArgOK := true
	flagSet := flag.NewFlagSet(common.GetBinaryName(), flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	cli := base.NewCLI(
		conslogging.ConsoleLogger{},
		base.WithVersion(Version),
		base.WithGitSHA(GitSha),
		base.WithBuiltBy(BuiltBy),
		base.WithDefaultBuildkitdImage(DefaultBuildkitdImage),
		base.WithDefaultInstallationName(DefaultInstallationName),
	)
	buildApp := subcmd.NewBuild(cli)
	rootApp := subcmd.NewRoot(cli, buildApp)

	for _, f := range cli.Flags().RootFlags(DefaultInstallationName, DefaultBuildkitdImage) {
		for _, name := range f.Names() {
			if _, ok := f.(*urfavecli.BoolFlag); ok {
				flagSet.Bool(name, false, "")
			} else {
				flagSet.String(name, "", "")
			}
		}
	}

	if envFileFromArgOK {
		err = flagSet.Parse(os.Args[1:])
		if err == nil {
			flagSet.Visit(func(f *flag.Flag) {
				if f.Name == eFlag.EnvFileFlag {
					envFile = f.Value.String()
					envFileOverride = true
				}
			})
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

	// The color package handles NO_COLOR natively. Only unset it for FORCE_COLOR.
	if isForceColor() {
		color.NoColor = false
	}

	padding := conslogging.DefaultPadding

	customPadding, ok := env.Lookup("TARGET_PADDING")
	if ok {
		targetPadding, err := strconv.Atoi(customPadding)
		if err == nil {
			padding = targetPadding
		}
	}

	fullTarget, ok := os.LookupEnv("EARTH_FULL_TARGET")
	if ok {
		v, err := strconv.ParseBool(fullTarget)
		if err != nil {
			fmt.Printf("Invalid value for EARTH_FULL_TARGET (%q): %s.\n", fullTarget, err.Error())
			return 1
		}

		if v {
			padding = conslogging.NoPadding
		}
	}

	logging := conslogging.Current(padding, conslogging.Info, cli.Flags().GithubAnnotations)

	cli.SetConsole(logging)
	earth := app.NewEarthApp(cli, rootApp, buildApp)

	return earth.Run(ctx, lastSignal)
}

// isForceColor returns true if the FORCE_COLOR environment variable is set to a truthy value.
// It uses a permissive boolean parser to support common truthy/falsy conventions.
func isForceColor() bool {
	forceColor := os.Getenv("FORCE_COLOR")
	if forceColor == "" {
		return false
	}

	v, err := parseBool(forceColor)
	if err != nil {
		fmt.Printf("read FORCE_COLOR from env: %q\n",
			forceColor)

		return false
	}

	return v
}

func parseBool(val string) (bool, error) {
	b, err := strconv.ParseBool(val)
	if err == nil {
		return b, nil
	}

	i, err := strconv.Atoi(val)
	if err == nil {
		return i != 0, nil
	}

	switch strings.ToLower(val) {
	case "yes", "y", "on":
		return true, nil
	case "no", "n", "off":
		return false, nil
	}

	return false, fmt.Errorf("invalid boolean value, "+
		"want (1, t, T, TRUE, true, True, 0, f, F, FALSE, false, False, yes, y, on, no, n, off, or integer): %q",
		val)
}
