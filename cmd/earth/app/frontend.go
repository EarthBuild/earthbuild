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
// This runs from `before`, where the *build subcommand's* flags have not been
// parsed - `--engine` is one of them - so the engine is read from the raw
// arguments. That makes that half a guess, and deliberately a timid one:
// anything unrecognised keeps the detection, and the cost of guessing wrong in
// that direction is the 116ms that was always being spent.
//
// **The subcommand is not guessed at**, because a scan cannot do it correctly.
// Root flags *are* parsed by the time this runs, so `subcommand` comes from the
// parser; looking for a command name among the raw arguments reads a global
// flag's value as a command, and the first match wins. `--git-username ls
// prune` answered for `ls` and handed `prune` a stub frontend.
func needsContainerFrontend(args []string, subcommand string, commands []string, engineEnv string) bool {
	engine := engineEnv
	if named, ok := engineFromArgs(args); ok {
		engine = named // the command line beats the environment, as everywhere else
	}

	if engine != "" && engine != nativeEngineName {
		return true
	}

	// A subcommand may want a daemon for its own reasons - `bootstrap` and
	// `prune` are *about* the daemon - so a named one needs it unless it is one
	// of the few that provably does not.
	// **The named subcommand decides**, rather than only being able to vote yes.
	// A file-only command that merely declined to return true fell through to
	// the target check below and was answered "detect" anyway, because
	// `ls ./examples` names no target - which is exactly right for a build and
	// meaningless for a command that does not take one.
	for _, c := range commands {
		if subcommand == c {
			return !readsOnlyFiles[c]
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

// readsOnlyFiles are the subcommands that touch no daemon, so detecting one
// before them is time spent on a capability they never reach.
//
// **Named rather than inferred, and short on purpose.** Everything this does not
// recognise keeps today's behaviour, which is to detect - the safe error for a
// decision made in `before`, where the subcommand's own flags are not parsed
// yet and all there is to go on is the raw argument list.
//
// `build` has been here since E871, where detecting a frontend cost 116ms of a
// 380ms cached native build. `ls` is the same argument: it reads an Earthfile,
// parses it and prints target names, and detection was 96ms of a 140ms
// invocation - two thirds of the command, for something it never touches.
var readsOnlyFiles = map[string]bool{
	"build": true,
	"ls":    true,
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

// engineChosen is the engine this invocation will use, decided from the raw
// arguments and the environment.
//
// **Needed before the build subcommand's flags are parsed**, which is where
// `before` runs and therefore where anything it decides has to come from. The
// flag's own default is native, so an invocation that names nothing gets
// native here too - stating that default in a second place is the cost of
// having to answer the question early, and the alternative is a caller that
// cannot tell "unnamed" from "buildkit".
func engineChosen(args []string, engineEnv string) string {
	if named, ok := engineFromArgs(args); ok && named != "" {
		return named // the command line beats the environment, as everywhere else
	}

	if engineEnv != "" {
		return engineEnv
	}

	return nativeEngineName
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
