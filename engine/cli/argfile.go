package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The files a project may keep beside its Earthfile.
//
// `.arg` supplies build arguments and `.secret` supplies secrets, both as
// `NAME=value` lines. They are how a project keeps values out of its Earthfile
// without typing them on every invocation, and this engine had neither: five of
// the corpus's invocations drive `dotenv.earth`, which exists to check them
// (E465).
//
// `.env` is deliberately absent from this pair. Since 0.7 it supplies the
// environment and **not** build arguments - the corpus asserts that a name found
// only in `.env` does not reach an `ARG` - so reading it here would reintroduce
// the behaviour that version removed.
const (
	defaultArgFile = ".arg"
	// defaultEnvFile is the file that used to supply build arguments. Read only
	// so that a project still keeping one can be told it no longer does.
	defaultEnvFile    = ".env"
	defaultSecretFile = ".secret"
)

// valuesFrom reads `NAME=value` lines from a file beside the Earthfile.
//
// A missing file is not an error and an unreadable one is: the first is a
// project that keeps no such file, which is most of them, and the second is a
// file the author wrote and this engine could not use - and silently building
// without values somebody put in a file is the shape of failure this engine is
// arranged against.
//
// Named explicitly by the caller, `required` says which of those it is.
func valuesFrom(dir, name string, required bool) (map[string]string, error) {
	path := filepath.Join(dir, name)

	f, err := os.Open(path) //nolint:gosec // a path in the project directory
	if err != nil {
		if os.IsNotExist(err) && !required {
			return nil, nil
		}

		// Named as the caller wrote it, not as it resolves.
		//
		// `os.Open` reports the whole path, and the corpus greps for
		// `open .this-should-fail: no such file or directory` - which is the
		// caller's own word for the file. A message that says
		// `/tmp/build-1234/.this-should-fail` answers a question about a
		// directory the caller never typed (E475).
		if path, ok := errors.AsType[*fs.PathError](err); ok {
			err = path.Err
		}

		return nil, fmt.Errorf("open %s: %w\n  looked in %s, which is the"+
			" project directory this build was given", name, err, dir)
	}

	defer func() { _ = f.Close() }()

	out := map[string]string{}
	scan := bufio.NewScanner(f)

	for line := 1; scan.Scan(); line++ {
		text := strings.TrimSpace(scan.Text())

		// Blank lines and comments, which every file of this shape has.
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf(
				"%s:%d is %q, which names no value"+
					"\n  each line is NAME=value", name, line, text)
		}

		// Quotes are the shell's, not the value's: `NAME="a b"` is `a b`, which
		// is what every reader of these files does and what an author writing
		// one expects.
		out[strings.TrimSpace(key)] = unquoted(strings.TrimSpace(value))
	}

	err = scan.Err()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	return out, nil
}

// unquoted removes one layer of matching quotes.
func unquoted(v string) string {
	if len(v) >= 2 {
		if q := v[0]; (q == '"' || q == '\'') && v[len(v)-1] == q {
			return v[1 : len(v)-1]
		}
	}

	return v
}

// beneath layers file values under the ones the caller gave.
//
// **The command line wins.** A file is a project's default and an argument is
// this invocation's instruction; the other way round, a value typed on the
// command line would be silently ignored because a file somewhere said
// otherwise.
func beneath(file, given map[string]string) map[string]string {
	if len(file) == 0 {
		return given
	}

	out := make(map[string]string, len(file)+len(given))

	maps.Copy(out, file)

	maps.Copy(out, given)

	return out
}

// withProjectFiles reads the project's `.arg` and `.secret` under what the
// caller gave.
//
// Both are optional and neither is a surprise: a project that keeps no such file
// is most projects. Where the caller *named* a path, a file that is not there is
// an error - they asked for it, and building without the values it would have
// held is the silent-wrong answer (E465).
func (o Options) withProjectFiles() (args, secrets map[string]string, err error) {
	argFile, named := namedFile(o.ArgFile, "ARG_FILE_PATH", defaultArgFile, o.env)

	fromArg, err := valuesFrom(o.Dir, argFile, named)
	if err != nil {
		return nil, nil, err
	}

	if fromArg == nil {
		o.reportDotEnv(argFile)
	}

	// By symmetry rather than by witness: the corpus drives only the argument
	// file from the environment, and an option pair where one half reads the
	// environment and the other does not is a surprise waiting for whoever
	// finds it.
	secretFile, namedSecret := namedFile(o.SecretFile, "SECRET_FILE_PATH", defaultSecretFile, o.env)

	fromSecret, err := valuesFrom(o.Dir, secretFile, namedSecret)
	if err != nil {
		return nil, nil, err
	}

	// A secret named on the command line as a file beats the project's, for the
	// reason the command line beats a file anywhere: it is this invocation's
	// instruction.
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	fromNamedFiles, err := secretsFromFiles(o.SecretFiles, home)
	if err != nil {
		return nil, nil, err
	}

	return beneath(fromArg, o.Args),
		beneath(fromSecret, beneath(fromNamedFiles, o.Secrets)), nil
}

// secretsFromFiles reads `--secret-file NAME=PATH` entries.
//
// One secret whose value is a file's contents, which is how a build gets a
// credential that is too long to type and must not be in the Earthfile. Distinct
// from the project's `.secret`, which holds many - and the two were conflated
// once, so the engine looked for a file called `SECRET3=~/my-secret-file`
// (E469).
//
// A missing file is always an error here: unlike `.secret`, every one of these
// was named by the caller. The alternative is a step receiving an empty
// credential and failing somewhere else with a message about authentication.
// runSecrets is what a *step* may ask for, which is what the plan was checked
// against.
//
// **Two maps for one question was the bug.** The interpreter is given the
// merged secrets - flags, `--secret-file` entries and the project's `.secret`
// file - and the executor was given `Options.Secrets`, which is only the
// flags. A build supplying `--secret-file MY=sec.txt` passed planning and then
// failed inside the step, naming a secret the caller had plainly supplied.
//
// A function rather than a field, so the two cannot drift again: whatever the
// plan was checked against is what the step is given.
func (o Options) runSecrets(merged map[string]string) map[string]string {
	if merged != nil {
		return merged
	}

	return o.Secrets
}

func secretsFromFiles(entries []string, home string) (map[string]string, error) {
	out := map[string]string{}

	for _, entry := range entries {
		name, path, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf(
				"--secret-file %s names no file"+
					"\n  write it as NAME=path", entry)
		}

		b, err := os.ReadFile(expandTilde(path, home))
		if err != nil {
			return nil, fmt.Errorf("--secret-file %s: %w", name, err)
		}

		out[name] = string(b)
	}

	return out, nil
}

// expandTilde resolves a leading `~` against a home directory.
//
// The corpus writes `~/my-secret-file` and so does anybody naming something in
// their own home. Only a leading one, and only followed by a separator or
// nothing: `~other/x` is another user's home, which this does not resolve, and
// silently reading the wrong file would be worse than leaving it alone.
func expandTilde(path, home string) string {
	if path == "~" {
		return home
	}

	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}

	return path
}

// namedFile settles which path to read, and whether somebody asked for it.
//
// Three sources in order: the flag, the environment, the project's usual name.
// The first two are *asked for* and a file that is not there is an error; the
// third is a convention and its absence is ordinary.
//
// The flag beats the environment, which the corpus drives directly - one
// invocation exports one path and passes another, and expects the passed one.
// A caller who exports a path and is quietly given `.arg` instead builds with
// the wrong values and is told nothing (E475).
func namedFile(flag, envSuffix, fallback string, env func(string) string) (path string, named bool) {
	if flag != "" {
		return flag, true
	}

	// Both spellings, as the builtin arguments have both: `EARTHLY_` is what
	// every existing script exports, `EARTH_` is this engine's own.
	for _, prefix := range []string{"EARTH_", "EARTHLY_"} {
		if v := env(prefix + envSuffix); v != "" {
			return v, true
		}
	}

	return fallback, false
}

// env reads a variable, this invocation's own answer first.
//
// A build driven by a terminal has only the process's environment and this is
// `os.Getenv` with a step in front of it. A build driven beside three others -
// the run gate - has an environment of its own, and `os.Setenv` would decide for
// its neighbours (E475).
func (o Options) env(name string) string {
	if v, ours := o.Env[name]; ours {
		return v
	}

	return os.Getenv(name)
}

// reportDotEnv says that a `.env` this project keeps decides nothing.
//
// It supplied build arguments until v0.7.0 of the Earthfile tooling and has not
// since. A project that still has one gets the values it expects from nowhere -
// `tests/dotenv.earth` asserts `test -z` for a name its `.env` sets - and a
// build that is silently missing values it was written to have is the failure
// this exists to prevent (E475).
//
// Only where the argument file is *absent*. An empty `.arg` is a project that
// knows where build arguments live now, and the corpus makes that the whole of
// the second case: `RUN touch .arg` and the warning is gone. **A diagnostic
// nobody can act on is one people learn to skip**, and here the action is
// exactly the file.
//
// Named, one line per name, sorted: a warning that says "your .env is ignored"
// leaves the reader to work out which of its names mattered.
func (o Options) reportDotEnv(argFile string) {
	if o.Out == nil {
		return
	}

	found, err := valuesFrom(o.Dir, defaultEnvFile, false)
	if err != nil || len(found) == 0 {
		return
	}

	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		fmt.Fprintf(o.Out, "unexpected env %q: as of v0.7.0, --build-arg values"+
			" must be defined in %s\n", name, argFile)
	}
}
