package main

import "strings"

// subcommands take flags after their name, as the reference writes them.
//
// `doc` and `ls` and nothing else: they take no build arguments, so a
// dash-prefixed word after one is a flag and there is nothing else it could be.
var subcommands = map[string]bool{"doc": true, "ls": true}

// hoistSubcommandFlags moves a subcommand's flags in front of it.
//
// **Go's flag package stops at the first non-flag argument**, so
// `earth-native doc --long` reads `doc` as the end of the flags and `--long` as
// an argument to it - reported as a build argument that is not one, which is a
// diagnostic about the wrong thing entirely. `earthly doc --long` is how the
// reference is written and how `tests/Earthfile` drives it.
//
// A *target* is left alone, and that is the whole of the rule: `--NAME=value`
// after one is a build argument, and hoisting it would turn
// `+build --VERSION=2` into a flag named VERSION.
func hoistSubcommandFlags(args []string) []string {
	at := -1

	for i, a := range args {
		if subcommands[a] {
			at = i

			break
		}

		// A flag's own value may look like anything, so only a leading dash is
		// evidence: the first bare word decides, and if it is not a subcommand
		// there is nothing to hoist.
		if !strings.HasPrefix(a, "-") && i > 0 && !strings.HasPrefix(args[i-1], "-") {
			return args
		}
	}

	if at < 0 {
		return args
	}

	out := make([]string, 0, len(args))
	out = append(out, args[:at]...)
	out = append(out, args[at+1:]...)

	return append(out, args[at])
}
