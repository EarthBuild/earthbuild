package cli

import "strings"

// EnvFileValues reads the project's `.env`, which supplies CLI settings.
//
// **It stopped supplying build arguments in v0.7.0 and never stopped supplying
// settings.** The corpus writes `EARTHLY_PUSH=1` into one and expects the build
// to push; this engine read the file only to warn about it, so that build did
// not push and the assertion inside the step failed.
//
// Same three sources as the argument and secret files, in the same order and for
// the same reason: the flag, then the environment, then the usual name - and a
// file *asked for* that is not there is an error, where the convention's absence
// is ordinary.
func EnvFileValues(dir, flagPath string, look func(string) string) (map[string]string, error) {
	name, named := namedFile(flagPath, "ENV_FILE_PATH", defaultEnvFile, look)

	return valuesFrom(dir, name, named)
}

// aSettingName reports whether a name in `.env` is one this engine reads as a
// setting rather than one it ignores.
//
// The prefix is the whole test, and it is intrinsic rather than a list of flags:
// a name spelled `EARTHLY_ANYTHING` was never a build-argument name somebody
// meant, so the "move it to .arg" advice is wrong about all of them whether or
// not this version happens to have the matching flag.
func aSettingName(name string) bool {
	for _, prefix := range EnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}
