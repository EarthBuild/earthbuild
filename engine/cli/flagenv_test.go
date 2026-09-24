package cli

import (
	"flag"
	"strings"
	"testing"
)

// TestAFlagNotGivenTakesItsValueFromTheEnvironment.
//
// **`.env` still decides CLI settings**, which the corpus asserts directly:
// `RUN echo EARTHLY_PUSH=1 > .env` and then a build with no `--push` that
// expects `EARTHLY_PUSH` to be `true` inside the step. Only `--arg-file-path`
// and `--secret-file-path` ever consulted the environment, each by hand.
//
// The name is the rule rather than a table: `arg-file-path` is `ARG_FILE_PATH`,
// which is the convention those two hand-written lookups already followed.
func TestAFlagNotGivenTakesItsValueFromTheEnvironment(t *testing.T) {
	t.Parallel()

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	push := fs.Bool("push", false, "")
	argFile := fs.String("arg-file-path", "", "")
	noOutput := fs.Bool("no-output", false, "")

	err := fs.Parse([]string{"--arg-file-path", ".given"})
	if err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"EARTHLY_PUSH":          "1",
		"EARTHLY_ARG_FILE_PATH": ".from-env",
		"EARTH_NO_OUTPUT":       "true",
	}

	err = ApplyEnvDefaults(fs, func(n string) string { return env[n] })
	if err != nil {
		t.Fatal(err)
	}

	if !*push {
		t.Error("EARTHLY_PUSH=1 did not enable --push")
	}

	if !*noOutput {
		t.Error("EARTH_NO_OUTPUT=true did not enable --no-output; both prefixes count")
	}

	// **The command line wins.** A caller who exports a path and passes another
	// means the one they passed - the corpus drives exactly that for
	// `--arg-file-path`, and silently preferring the export builds with the
	// wrong values and says nothing.
	if *argFile != ".given" {
		t.Errorf("--arg-file-path is %q; the command line must beat the environment", *argFile)
	}
}

// EARTH_ beats EARTHLY_: it is this engine's own spelling, and a machine that
// has both set meant the specific one.
func TestThisEnginesOwnPrefixWins(t *testing.T) {
	t.Parallel()

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	dir := fs.String("dir", ".", "")

	err := fs.Parse(nil)
	if err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"EARTH_DIR": "/mine", "EARTHLY_DIR": "/theirs"}

	err = ApplyEnvDefaults(fs, func(n string) string { return env[n] })
	if err != nil {
		t.Fatal(err)
	}

	if *dir != "/mine" {
		t.Errorf("--dir is %q, want /mine", *dir)
	}
}

// A value the flag cannot take is the caller's mistake and is reported as one,
// naming the variable - not the flag, which they did not write.
func TestAnUnusableValueNamesTheVariable(t *testing.T) {
	t.Parallel()

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(discard{})
	fs.Bool("push", false, "")

	err := fs.Parse(nil)
	if err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"EARTHLY_PUSH": "yes please"}

	err = ApplyEnvDefaults(fs, func(n string) string { return env[n] })
	if err == nil {
		t.Fatal("a value the flag cannot take was accepted")
	}

	if !strings.Contains(err.Error(), "EARTHLY_PUSH") {
		t.Errorf("the failure reads %q, without the variable that caused it", err)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
