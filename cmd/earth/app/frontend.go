package app

import (
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/EarthBuild/earthbuild/internal/env"
)

// nativeEngineName is the engine that needs no container daemon. It is also the
// default, which is why the saving applies to most builds rather than a few.
const nativeEngineName = "native"

// needsContainerFrontend reports whether this invocation can possibly use a
// Docker or Podman frontend.
//
// Detecting one costs about 116ms - a third of the wall clock of a fully cached
// build - because it runs the candidate binaries to see which answers. The
// native engine never consults the result: `engine/` contains no reference to a
// container frontend at all, and every consumer is on the buildkit path (E871).
//
// This runs from `before`, where the build subcommand's flags have not been
// parsed, so it reads the raw arguments. That makes it a guess, and it is
// deliberately a timid one: anything unrecognised - an unknown engine, another
// subcommand, no arguments - keeps the detection. The cost of guessing wrong in
// that direction is the 116ms that was always being spent.
func needsContainerFrontend(args, commands []string, engineEnv string) bool {
	engine := engineEnv
	if named, ok := engineFromArgs(args); ok {
		engine = named // the command line beats the environment, as everywhere else
	}

	if engine != "" && engine != nativeEngineName {
		return true
	}

	// A subcommand other than `build` may want a daemon for its own reasons -
	// `bootstrap` and `prune` are about the daemon - so only a build qualifies.
	for _, a := range args {
		for _, c := range commands {
			if a == c && c != "build" {
				return true
			}
		}
	}

	// A build names a target, and nothing else on the line does. Requiring one
	// keeps `earth` with no arguments, and anything else unforeseen, on the
	// path that detects.
	for _, a := range args {
		if strings.Contains(a, "+") && !strings.HasPrefix(a, "-") {
			return false
		}
	}

	return true
}

// engineFromArgs finds `--engine <name>` or `--engine=<name>`, reporting whether
// one was given at all - an absent flag and an empty one differ, because only
// the first defers to the environment.
func engineFromArgs(args []string) (string, bool) {
	for i, a := range args {
		if name, ok := strings.CutPrefix(a, "--engine="); ok {
			return name, true
		}

		if a == "--engine" && i+1 < len(args) {
			return args[i+1], true
		}
	}

	return "", false
}

// commandNames lists what the CLI will accept as a subcommand, so the decision
// above compares against the real set rather than a copy that drifts.
func commandNames(cmds []*cli.Command) []string {
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}

	return names
}

// engineEnv reads the engine the environment asks for, matching the flag's own
// Sources so `EARTHLY_ENGINE` keeps working alongside `EARTH_ENGINE`.
func engineEnv() string {
	if v, ok := env.Lookup("ENGINE"); ok {
		return v
	}

	return ""
}
