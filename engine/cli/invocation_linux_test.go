//go:build linux && integration

package cli_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
	"github.com/EarthBuild/earthbuild/internal/corpus"
)

// readCorpusFile reads a file from the corpus tree.
func readCorpusFile(t *testing.T, rel string) string {
	t.Helper()

	root := os.Getenv("EARTH_CORPUS_DIR")
	if root == "" {
		root = filepath.Join("..", "..")
	}

	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Skipf("no corpus here: %v", err)
	}

	return string(b)
}

// passable turns an invocation's extra arguments into build options.
//
// Returns the options rather than a widening tuple: there were three of them and
// then five, and a signature that grows a return value per flag is one nobody
// can read (E465).
//
// The tree passes eight distinct options across its invocations, and they are
// three different things:
//
//   - values the build needs - `--build-arg K=V`, `--secret K=V` - which become
//     options here;
//   - instructions about the *invocation* rather than the build - `--no-output`,
//     `--ci`, `--allow-privileged` - which this gate either does by default or
//     refuses everywhere, and which change nothing it can observe;
//   - options this gate cannot pass, which are reported by name rather than
//     dropped.
//
// The third case is why this returns a reason. **An invocation driven without an
// option it was given is a different invocation**, and counting its failure
// against the engine is the harness blaming the subject for its own omission
// (E455).
func passable(in corpus.Invocation) (opts cli.Options, why string) {
	if in.Pre != "" {
		return cli.Options{}, "--pre_command " + strconv.Quote(in.Pre) +
			", which this gate has no shell to run"
	}

	args, secrets := map[string]string{}, map[string]string{}

	var (
		noCache, execStats  bool
		argFile, secretFile string
		secretFiles         []string
		versionFlags        []string
	)

	for i := 0; i < len(in.Extra); i++ {
		// `--secret=NAME=value` as well as `--secret NAME=value`: one thing to
		// the option parser and two to a switch on the whole word, and the tree
		// writes both (E463).
		flag, joined, isJoined := strings.Cut(in.Extra[i], "=")

		// `--version-flag-overrides=a,b` is only ever written joined, and names
		// a comma-separated list rather than one value.
		if isJoined && flag == "--version-flag-overrides" {
			versionFlags = append(versionFlags, strings.Split(joined, ",")...)

			continue
		}

		if isJoined && (flag == "--build-arg" || flag == "--secret") {
			name, value, ok := strings.Cut(joined, "=")
			if !ok {
				return cli.Options{}, flag + " " + joined + " names no value"
			}

			if flag == "--secret" {
				secrets[name] = value
			} else {
				args[name] = value
			}

			continue
		}

		switch flag := in.Extra[i]; flag {
		case "--build-arg", "--secret":
			if i+1 >= len(in.Extra) {
				return cli.Options{}, flag + " with no value"
			}

			i++

			name, value, ok := strings.Cut(in.Extra[i], "=")
			if !ok {
				// `--secret NAME` takes its value from the environment, which
				// is how the tree passes one: it writes `ENV SECRET1=foo`
				// before the invocation. A name the environment does not have
				// is *not attempted* rather than passed as empty - an empty
				// secret is a different secret (E463).
				if flag != "--secret" {
					return cli.Options{}, flag + " " + in.Extra[i] + " names no value"
				}

				name = in.Extra[i]

				value, ok = os.LookupEnv(name)
				if !ok {
					return cli.Options{}, "--secret " + name +
						", which this environment does not have"
				}
			}

			if flag == "--secret" {
				secrets[name] = value
			} else {
				args[name] = value
			}

		case "--no-cache":
			noCache = true

		case "--exec-stats":
			execStats = true

		case "--secret-file":
			// One secret whose value is a file's contents, which is not the
			// project's `.secret` - the two were conflated when that was
			// written (E469).
			if i+1 >= len(in.Extra) {
				return cli.Options{}, flag + " with no value"
			}

			i++

			secretFiles = append(secretFiles, in.Extra[i])

		case "--arg-file-path", "--secret-file-path":
			// The project's own files, which the engine reads now (E465). Their
			// paths are relative to the project directory, which is where the
			// gate writes the file under test.
			if i+1 >= len(in.Extra) {
				return cli.Options{}, flag + " with no path"
			}

			i++

			if flag == "--arg-file-path" {
				argFile = in.Extra[i]
			} else {
				secretFile = in.Extra[i]
			}

		case "--version-flag-overrides":
			// The unjoined spelling, for completeness: the tree writes the
			// joined one.
			//
			// Passed to the engine now rather than recognised and dropped. It
			// was the latter for one flag matched by its exact value - a gate
			// that answers for a feature it has not been given is claiming
			// something it has not checked - and the engine has somewhere to
			// put it since E473.
			if i+1 >= len(in.Extra) {
				return cli.Options{}, flag + " with no value"
			}

			i++

			versionFlags = append(versionFlags, strings.Split(in.Extra[i], ",")...)

		case "doc", "ls":
			// A word rather than a flag: the reading commands, which take no
			// target and build nothing. Dispatched by the caller, which is why
			// this only has to stop them being read as an option nobody can
			// pass (E474).

		case "--long":
			opts.Long = true

		case "--no-output", "--ci", "--allow-privileged", "--verbose", "--interactive":
			// About the invocation, not the build.

		default:
			return cli.Options{}, flag
		}
	}

	opts.Args, opts.Secrets = args, secrets
	opts.NoCache, opts.ExecStats = noCache, execStats
	opts.ArgFile, opts.SecretFile, opts.SecretFiles = argFile, secretFile, secretFiles
	opts.VersionFlags = versionFlags
	opts.Env = in.Env

	return opts, ""
}

// verbOf names the reading command an invocation asks for, empty for a build.
//
// A separate pass rather than another return from `passable`, which already
// answers two questions: *a signature that grows a return per fact* is how the
// guest's step result went from one value to four before it became a struct
// (E446).
func verbOf(in corpus.Invocation) string {
	for _, a := range in.Extra {
		if a == "doc" || a == "ls" {
			return a
		}
	}

	return ""
}
