package subcmd

import (
	"os"

	"github.com/urfave/cli/v3"
)

func (b *Build) buildFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringSliceFlag{
			Name:        "platform",
			Sources:     cli.EnvVars("EARTHLY_PLATFORMS"),
			Usage:       "Specify the target platform to build for or this can be read from ENV VAR",
			Destination: &b.platformsStr,
		},
		&cli.StringSliceFlag{
			Name:        "build-arg",
			Sources:     cli.EnvVars("EARTHLY_BUILD_ARGS"),
			Usage:       "A build arg override, specified as <key>=[<value>]",
			Destination: &b.buildArgs,
			Hidden:      true, // Deprecated
		},
		&cli.StringSliceFlag{
			Name:        "secret",
			Aliases:     []string{"s"},
			Sources:     cli.EnvVars("EARTHLY_SECRETS"),
			Usage:       "A secret override, specified as <key>=[<value>]",
			Destination: &b.secrets,
		},
		&cli.StringSliceFlag{
			Name:        "secret-file",
			Sources:     cli.EnvVars("EARTHLY_SECRET_FILES"),
			Usage:       "A secret override, specified as <key>=<path>",
			Destination: &b.secretFiles,
		},
		&cli.StringSliceFlag{
			Name:        "cache-from",
			Sources:     cli.EnvVars("EARTHLY_CACHE_FROM"),
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
