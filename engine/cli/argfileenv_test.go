package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// The environment can name the project's argument file.
//
// `tests/Earthfile` drives the same build three ways and expects one answer:
// with `--arg-file-path .some-other-arg`, with
// `export EARTHLY_ARG_FILE_PATH=.some-other-arg`, and with both - where the flag
// is the one that counts. A caller who exports a path and is quietly given `.arg`
// builds with the wrong values and is told nothing (E475).
func TestTheEnvironmentCanNameTheArgumentFile(t *testing.T) {
	dir := argProject(t, ".some-other-arg", "GREETING=hello\n")

	t.Setenv("EARTHLY_ARG_FILE_PATH", ".some-other-arg")

	if got := greetingOf(t, cli.Options{Dir: dir}); got != "hello" {
		t.Errorf("the step runs with GREETING=%q, and the exported file says hello", got)
	}
}

// The flag outranks the environment, which is what the tree calls precedence.
func TestTheFlagOutranksTheExportedArgumentFile(t *testing.T) {
	dir := argProject(t, ".some-other-arg", "GREETING=hello\n")

	err := os.WriteFile(filepath.Join(dir, ".ignored-arg"),
		[]byte("GREETING=wrong\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("EARTHLY_ARG_FILE_PATH", ".ignored-arg")

	got := greetingOf(t, cli.Options{Dir: dir, ArgFile: ".some-other-arg"})
	if got != "hello" {
		t.Errorf("the step runs with GREETING=%q; the flag names the file that"+
			" says hello and the environment names the other one", got)
	}
}

// A path the environment names and the project does not have is an error.
//
// The same rule the flag has, for the same reason: the caller asked for that
// file, and building without the values it would have held is the silent-wrong
// answer. The tree asserts both spellings separately and both messages
// (E465, E475).
func TestAnExportedArgumentFileThatIsNotThereIsAnError(t *testing.T) {
	dir := argProject(t, ".arg", "GREETING=hello\n")

	t.Setenv("EARTHLY_ARG_FILE_PATH", ".this-too-should-fail")

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: "+main", DryRun: true,
	})
	if err == nil {
		t.Fatal("a file the caller named and the project does not have was" +
			" passed over, so the build used values nobody asked for")
	}

	// The tree greps the message for exactly this, so the wording is part of
	// what the engine promises rather than a detail of it.
	if want := "open .this-too-should-fail: no such file or directory"; !strings.Contains(err.Error(), want) {
		t.Errorf("refused with %q, and the corpus greps for %q", err, want)
	}
}

// argProject writes an Earthfile that echoes an argument, and one values file.
func argProject(t *testing.T, name, contents string) string {
	t.Helper()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "Earthfile"),
		[]byte("VERSION 0.8\n\nmain:\n    FROM alpine:3.22\n"+
			"    ARG GREETING=none\n    RUN echo $GREETING\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

// greetingOf plans a build and reports what the step was handed.
func greetingOf(t *testing.T, o cli.Options) string {
	t.Helper()

	o.Target, o.DryRun = "+main", true

	var out strings.Builder

	o.Out = &out

	err := cli.Run(context.Background(), o)
	if err != nil {
		t.Fatalf("planning: %v", err)
	}

	for line := range strings.SplitSeq(out.String(), "\n") {
		if _, after, found := strings.Cut(line, "echo "); found {
			return strings.TrimSpace(after)
		}
	}

	t.Fatalf("the plan ran no echo at all:\n%s", out.String())

	return ""
}

// An invocation may carry its own environment.
//
// The run gate drives four builds at once in one process, so a variable the tree
// exports for one invocation cannot be set with `os.Setenv` without deciding it
// for the other three. Two corpus invocations pass
// `--pre_command="export EARTHLY_ARG_FILE_PATH=..."`, and a gate that ignored
// them would be running a different invocation and reporting the difference as
// the engine's (E475).
//
// The invocation's own environment beats the process's, because it is the
// nearer statement of what this build was told.
func TestAnInvocationCarriesItsOwnEnvironment(t *testing.T) {
	dir := argProject(t, ".some-other-arg", "GREETING=hello\n")

	err := os.WriteFile(filepath.Join(dir, ".arg"),
		[]byte("GREETING=wrong\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("EARTHLY_ARG_FILE_PATH", "")

	got := greetingOf(t, cli.Options{
		Dir: dir,
		Env: map[string]string{"EARTHLY_ARG_FILE_PATH": ".some-other-arg"},
	})
	if got != "hello" {
		t.Errorf("the step runs with GREETING=%q; the invocation's own"+
			" environment names the file that says hello", got)
	}
}

// And it beats the process's, which is the point of having it.
func TestTheInvocationsEnvironmentBeatsTheProcesss(t *testing.T) {
	dir := argProject(t, ".some-other-arg", "GREETING=hello\n")

	err := os.WriteFile(filepath.Join(dir, ".ignored-arg"),
		[]byte("GREETING=wrong\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("EARTHLY_ARG_FILE_PATH", ".ignored-arg")

	got := greetingOf(t, cli.Options{
		Dir: dir,
		Env: map[string]string{"EARTHLY_ARG_FILE_PATH": ".some-other-arg"},
	})
	if got != "hello" {
		t.Errorf("the step runs with GREETING=%q; the process environment"+
			" decided for an invocation that said otherwise", got)
	}
}
