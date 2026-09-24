package cli_test

import (
	"bytes"
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

func project(t *testing.T, earthfile string, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, testEarthfile), []byte(earthfile), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for name, body := range files {
		p := filepath.Join(dir, name)
		err := os.MkdirAll(filepath.Dir(p), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(p, []byte(body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

// A missing Earthfile is the first thing a new user hits, so the message has to
// say where it looked rather than reporting a bare ENOENT.
func TestMissingEarthfileSaysWhereItLooked(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	dir := t.TempDir()

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{Dir: dir, Target: testTarget, Out: &out})
	if err == nil {
		t.Fatal("a directory with no Earthfile was accepted")
	}

	for _, want := range []string{testEarthfile, dir} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// A parse error must carry the line, not just "syntax error".
func TestParseErrorsReachTheUser(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	dir := project(t, "VERSION 0.8\n\nbuild:\n    NONSENSE foo\n", nil)

	err := cli.Run(context.Background(), cli.Options{Dir: dir, Target: testTarget, Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("an Earthfile with an unknown command was accepted")
	}
}

// The target list is what a user wants when they have mistyped one.
func TestUnknownTargetListsWhatExists(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	dir := project(t, "VERSION 0.8\n\nbuild:\n    FROM alpine\n\ntest:\n    FROM alpine\n", nil)

	err := cli.Run(context.Background(), cli.Options{Dir: dir, Target: "biuld", Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("an unknown target was accepted")
	}

	for _, want := range []string{testTarget, "test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not list %q:\n%s", want, err)
		}
	}
}

// An unsupported construct is refused before anything runs, and says which
// engine can do it. This is I10 reaching the person at the terminal.
//
//nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
func TestUnsupportedConstructIsRefusedWithAnAlternative(t *testing.T) {
	// `RUN --privileged`, because LOCALLY and now WITH DOCKER are supported. The
	// construct here only has to be one the engine genuinely cannot evaluate,
	// and this test has outlived two of them - which is the point of it.
	dir := project(t, "VERSION 0.8\n\nbuild:\n    FROM alpine\n    RUN --privileged true\n", nil)

	err := cli.Run(context.Background(), cli.Options{Dir: dir, Target: testTarget, Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("RUN --privileged was accepted")
	}

	for _, want := range []string{"--privileged", "buildkit"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q:\n%s", want, err)
		}
	}
}

// DryRun resolves the whole build - parse, context digests, graph - without a
// sandbox, which is what makes these tests runnable anywhere and what a user
// wants when checking an Earthfile on a machine with no VM.
//
//nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
func TestDryRunReportsThePlanWithoutRunningIt(t *testing.T) {
	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    COPY src/main.go /app/
    RUN go build
    SAVE ARTIFACT /app/out AS LOCAL dist/out
`, map[string]string{"src/main.go": "package main"})

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	text := out.String()

	// Every step, in order, attributed to its line.
	for _, want := range []string{"FROM alpine:3.22", "COPY src/main.go", "RUN go build", testLocPrefix} {
		if !strings.Contains(text, want) {
			t.Errorf("plan does not mention %q:\n%s", want, text)
		}
	}

	// And what it would produce.
	if !strings.Contains(text, "dist/out") {
		t.Errorf("plan does not mention the artifact:\n%s", text)
	}
}

// A build whose every step runs on this machine needs no sandbox.
//
// It booted one anyway - a VM started, used for nothing, and torn down - and
// worse, the build *failed* on a machine with no sandbox installed. A LOCALLY
// target is precisely the thing someone without a container runtime can run, so
// requiring one to run it is backwards.
func TestALocalOnlyBuildNeedsNoSandbox(t *testing.T) {
	dir := project(t, `VERSION 0.8

report:
    LOCALLY
    RUN `+shPath(t)+` -c "echo ran > out.txt"
`, nil)

	// EARTH_GUESTD names a file that does not exist, so any attempt to start a
	// sandbox fails. The build must not attempt one.
	t.Setenv("EARTH_GUESTD", filepath.Join(dir, "no-such-guest"))

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{Dir: dir, Target: "report", Out: &out})
	if err != nil {
		t.Fatalf("a local-only build required a sandbox: %v", err)
	}

	_, err = os.Stat(filepath.Join(dir, testArtefact))
	if err != nil {
		t.Errorf("the step did not run: %v", err)
	}
}

// A build with even one sandboxed step still needs the sandbox, and says so
// when it cannot have one.
//
// The step is unique to this run, because the sandbox now starts on first use
// and a build whose every step is an L1 hit is entitled to succeed without one.
// With a fixed command this test passed or failed on whether the developer's
// cache happened to hold `RUN true` - which it did, from an unrelated build
// earlier the same day.
func TestAMixedBuildStillNeedsASandbox(t *testing.T) {
	dir := project(t, `VERSION 0.8

report:
    LOCALLY
    RUN `+shPath(t)+` -c "true"

build:
    FROM alpine:3.22
    RUN echo `+t.Name()+`-`+strconv.FormatInt(time.Now().UnixNano(), 10)+`
`, nil)

	t.Setenv("EARTH_GUESTD", filepath.Join(dir, "no-such-guest"))

	err := cli.Run(context.Background(), cli.Options{Dir: dir, Target: testTarget, Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("a sandboxed build ran without a sandbox")
	}
}

func shPath(t *testing.T) string {
	t.Helper()

	p, err := osexec.LookPath("sh")
	if err != nil {
		t.Skip("no shell here")
	}

	return p
}
