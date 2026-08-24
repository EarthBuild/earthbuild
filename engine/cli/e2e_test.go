package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// TestBuildsThatActuallyRun exercises the whole path - parse, plan, schedule,
// execute, export - for one construct at a time, and asserts what ended up on
// disk.
//
// Every case is a LOCALLY target, which is what makes this affordable: a host
// step needs no sandbox, no image and no network, so the suite runs on any
// machine in milliseconds and covers execution rather than only planning.
//
// The corpus measures what *plans*. It was blind to a build that planned
// correctly and then demanded a sandbox it never used, and it is blind by
// construction to anything that goes wrong after the graph exists.
func TestBuildsThatActuallyRun(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	sh := shPath(t)

	for _, tc := range cases(t) {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sandboxOnly {
				t.Skip("this construct has no meaning on a host target")
			}

			// On this machine the artifact is simply written where the build
			// runs: a host target has no image to carry it out of.
			body := fill(strings.ReplaceAll(tc.body, "FILE", tc.file), sh)

			version := tc.version
			if version == "" {
				version = "VERSION 0.8"
			}

			src := version + "\n\nbuild:\n    LOCALLY\n" + body +
				fill(strings.ReplaceAll(functionBlock, "FILE", tc.file), sh)

			dir := project(t, src, tc.context)

			var out bytes.Buffer

			err := cli.Run(context.Background(), cli.Options{
				Dir: dir, Target: testTarget, Out: &out,
			})
			if err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}

			b, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}

			if got := string(b); got != tc.want {
				t.Errorf("%s contains %q, want %q", tc.file, got, tc.want)
			}
		})
	}
}

// A failing step fails the build, and says which command and what it printed.
func TestAFailingStepStopsTheBuild(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	sh := shPath(t)

	dir := project(t, `VERSION 0.8

build:
    LOCALLY
    RUN `+sh+` -c "echo before > out.txt"
    RUN `+sh+` -c "echo the-reason >&2; exit 3"
    RUN `+sh+` -c "echo after >> out.txt"
`, nil)

	err := cli.Run(context.Background(), cli.Options{Dir: dir, Target: testTarget, Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("a build with a failing step reported success")
	}

	for _, want := range []string{"3", testLocPrefix, "the-reason"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%s", want, err)
		}
	}

	// The step after the failure did not run.
	b, err := os.ReadFile(filepath.Join(dir, testArtefact))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(b), "after") {
		t.Error("a step after the failing one ran anyway")
	}
}
