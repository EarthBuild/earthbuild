package cli

import (
	"flag"
	"fmt"
	"strings"
)

// EnvPrefixes are the two spellings a setting may arrive under, in the order
// they are consulted.
//
// `EARTH_` is this engine's own and beats `EARTHLY_`, which is what existing
// scripts export; a machine with both set meant the specific one. The pair is
// the same one the builtin arguments carry.
var EnvPrefixes = []string{"EARTH_", "EARTHLY_"}

// EnvNameOf is the variable a flag answers to: the flag's own name, upper-cased,
// with `-` written `_`.
//
// A rule rather than a table, because a table drifts from the flags it claims to
// describe. It is also the convention already in use - `--arg-file-path` was
// read from `ARG_FILE_PATH` by hand before anything general existed.
func EnvNameOf(flagName string) string {
	return strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

// ApplyEnvDefaults gives every flag the caller did not write a value from the
// environment, if the environment has one.
//
// **This is how a project's `.env` still decides CLI settings.** `EARTHLY_PUSH=1`
// in `.env` means the build pushes, which the corpus asserts by reading
// `EARTHLY_PUSH` from inside a step - and only two flags consulted the
// environment before, each by hand.
//
// Precedence is command line, then environment, then the flag's default. A
// caller who exports one path and passes another means the one they passed:
// quietly preferring the export builds with the wrong values and says nothing
// (E475).
//
// A value the flag cannot take is a failure rather than a fallback, and it names
// the *variable*: the caller did not write the flag, so a message about the flag
// sends them looking in the wrong place.
func ApplyEnvDefaults(fs *flag.FlagSet, look func(string) string) error {
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	var failed error

	fs.VisitAll(func(f *flag.Flag) {
		if failed != nil || given[f.Name] {
			return
		}

		for _, prefix := range EnvPrefixes {
			name := prefix + EnvNameOf(f.Name)

			v := look(name)
			if v == "" {
				continue
			}

			err := fs.Set(f.Name, v)
			if err != nil {
				failed = fmt.Errorf("%s=%q: %w"+
					"\n  it sets --%s, which cannot take that value", name, v, err, f.Name)
			}

			return
		}
	})

	return failed
}
