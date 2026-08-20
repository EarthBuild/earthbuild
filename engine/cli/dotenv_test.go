package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A `.env` whose names reach nothing says so, once, by name.
//
// `.env` used to supply build arguments and has not since v0.7.0 of the
// Earthfile tooling. A project that still keeps one gets the values it expects
// from nowhere: `tests/dotenv.earth` asserts `test -z "$TEST_IN_DOTENV"` for a
// name the file sets, so the silence is correct and the *silence about the
// silence* is not (E475).
//
// The tree greps the output for the wording, so it is part of what the engine
// promises rather than a detail of it.
func TestADotEnvWithNoArgFileIsReported(t *testing.T) {
	t.Parallel()

	dir := dotEnvProject(t)

	out := runFor(t, cli.Options{Dir: dir})

	const want = `unexpected env "TEST_IN_DOTENV": as of v0.7.0,` +
		` --build-arg values must be defined in .arg`

	if !strings.Contains(out, want) {
		t.Errorf("the build printed\n%s\nand the tree greps for\n  %s", out, want)
	}
}

// A project that has moved on is not told about it.
//
// `RUN touch .arg` is the whole of the tree's second case: an empty `.arg` is a
// project that knows where build arguments live now, and the warning would be
// noise on every build forever. **A diagnostic that cannot be acted on is one
// people learn to skip**, and the action here is exactly the file's existence.
func TestADotEnvBesideAnArgFileIsNotReported(t *testing.T) {
	t.Parallel()

	dir := dotEnvProject(t)

	if err := os.WriteFile(filepath.Join(dir, ".arg"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if out := runFor(t, cli.Options{Dir: dir}); strings.Contains(out, "unexpected env") {
		t.Errorf("the build warned about .env although the project has an"+
			" .arg:\n%s", out)
	}
}

// dotEnvProject writes an Earthfile and a `.env` that no longer decides
// anything.
func dotEnvProject(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"),
		[]byte("VERSION 0.8\n\nmain:\n    FROM alpine:3.22\n"+
			"    ARG TEST_IN_DOTENV\n    RUN echo [$TEST_IN_DOTENV]\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("TEST_IN_DOTENV=this-should-not-appear-as-a-build-arg\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

// runFor plans a build and returns everything it printed.
func runFor(t *testing.T, o cli.Options) string {
	t.Helper()

	var out strings.Builder

	o.Target, o.DryRun, o.Out = "+main", true, &out

	if err := cli.Run(context.Background(), o); err != nil {
		t.Fatalf("planning: %v", err)
	}

	return out.String()
}

// And the value itself reaches nothing, which is what the warning is about.
func TestADotEnvSuppliesNoBuildArgument(t *testing.T) {
	t.Parallel()

	out := runFor(t, cli.Options{Dir: dotEnvProject(t)})

	if strings.Contains(out, "this-should-not-appear-as-a-build-arg") {
		t.Errorf("a .env value reached a step:\n%s", out)
	}
}
