package subcmd

import (
	"os"

	"github.com/EarthBuild/earthbuild/cmd/earth/flag"
	"github.com/urfave/cli/v3"
)

func (b *Build) buildFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "engine",
			Sources: flag.EarthEnvVars("ENGINE"),
			// No backticks: urfave/cli reads the first backticked word as the
			// value's placeholder name, so `buildkit` here made the help read
			// "--engine buildkit" as though that were a type.
			Usage: "Which build engine runs the build: native (the default) or buildkit",
			// **Native by default on this branch, deliberately.**
			//
			// Not because it is ready - it cannot run LOCALLY, cannot emulate
			// another architecture, and needs a privilege Ubuntu 24.04 does not
			// grant (E596). Because every job in this repository's CI then
			// exercises it, and a suite that has been run against buildkit for
			// years is a better differential than any test written on purpose.
			// What it reports is where the two engines disagree, which is the
			// thing worth knowing before either becomes a default anywhere that
			// ships (E603).
			Value:       "native",
			Destination: &b.cli.Flags().Engine,
		},
		&cli.StringSliceFlag{
			Name:        "platform",
			Sources:     flag.EarthEnvVars("PLATFORMS"),
			Usage:       "Specify the target platform to build for or this can be read from ENV VAR",
			Destination: &b.platformsStr,
		},
		&cli.StringSliceFlag{
			Name:        "build-arg",
			Sources:     flag.EarthEnvVars("BUILD_ARGS"),
			Usage:       "A build arg override, specified as <key>=[<value>]",
			Destination: &b.buildArgs,
			Hidden:      true, // Deprecated
		},
		&cli.StringSliceFlag{
			Name:        "secret",
			Aliases:     []string{"s"},
			Sources:     flag.EarthEnvVars("SECRETS"),
			Usage:       "A secret override, specified as <key>=[<value>]",
			Destination: &b.secrets,
		},
		&cli.StringSliceFlag{
			Name:        "secret-file",
			Sources:     flag.EarthEnvVars("SECRET_FILES"),
			Usage:       "A secret override, specified as <key>=<path>",
			Destination: &b.secretFiles,
		},
		&cli.StringSliceFlag{
			Name:        "cache-from",
			Sources:     flag.EarthEnvVars("CACHE_FROM"),
			Usage:       "Remote docker image tags to use as readonly explicit cache (experimental)",
			Destination: &b.cacheFrom,
			Hidden:      true, // Experimental
		},
	}
}

// HiddenFlags returns the hidden build flags.
func (b *Build) HiddenFlags() []cli.Flag {
	_, isAutocomplete := os.LookupEnv("COMP_LINE")

	flags := b.buildFlags()
	if isAutocomplete {
		// Don't hide the build flags for autocomplete.
		return flags
	}

	for _, flag := range flags {
		switch f := flag.(type) {
		case *cli.StringSliceFlag:
			f.Hidden = true
		case *cli.StringFlag:
			f.Hidden = true
		case *cli.BoolFlag:
			f.Hidden = true
		case *cli.IntFlag:
			f.Hidden = true
		}
	}

	return flags
}
