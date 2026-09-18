package app

import "github.com/urfave/cli/v3"

// readsOnlyFiles are the subcommands that touch no container runtime, so
// detecting one before them is time spent on a capability they never reach.
//
// **Named rather than inferred, and short on purpose.** Everything this does not
// recognise keeps today's behaviour, which is to detect - the safe error for a
// decision made in `before`, where the subcommand's own flags are not parsed yet
// and all there is to go on is the raw argument list.
//
// `ls` reads an Earthfile, parses it and prints target names. It starts no
// container, and the probe before it - `docker ps`, then an info-style question
// about rootless mode, user namespaces and security options - was measured at
// 96ms of a 270ms invocation on this repository's own Earthfile.
var readsOnlyFiles = map[string]bool{
	"ls": true,
}

// needsContainerFrontend reports whether this invocation should look for docker
// or podman before it runs.
//
// It reads the raw arguments because the decision is made in `before`, where the
// subcommand's flags are not parsed yet.
//
// **The first recognised subcommand decides.** Anything unrecognised - a bare
// target, no arguments at all, a flag this does not know - is answered "detect",
// which is what every invocation did before this existed.
func needsContainerFrontend(args, commands []string) bool {
	for _, a := range args {
		for _, c := range commands {
			if a == c {
				return !readsOnlyFiles[c]
			}
		}
	}

	return true
}

// commandNames is the set of subcommands this application defines, so the gate
// above can tell one from a target or a flag value.
func commandNames(cmds []*cli.Command) []string {
	out := make([]string, 0, len(cmds))

	for _, c := range cmds {
		out = append(out, c.Name)
		out = append(out, c.Aliases...)
	}

	return out
}
