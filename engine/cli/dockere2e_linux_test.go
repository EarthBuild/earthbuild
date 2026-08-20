//go:build linux && integration

package cli_test

import (
	"bytes"
	"context"
	"os"
	osexec "os/exec"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A build with a WITH DOCKER block runs, through the whole engine.
//
// Everything before this proved a seam: the daemon starts, the step reaches it,
// the interpreter stamps the flag, the scheduler honours it. This proves the
// path - parse, plan, schedule, execute, export - with a real base image, a real
// daemon, and a real `docker` client asking that daemon a question only a
// running one can answer.
//
// The image is `docker:27-cli`, which carries a client and **no daemon**. That
// is not incidental: E368 decided the daemon runs beside the step rather than
// inside it, so an image needing only a client is the design's own claim, and an
// image that shipped `dockerd` would let a wrong build pass by accident.
func TestABuildWithADockerBlockRuns(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	if _, err := osexec.LookPath("dockerd"); err != nil {
		t.Skipf("no dockerd on this machine: %v", err)
	}

	guest := buildGuestd(t)
	cache := storeDir(t)

	dir := project(t, `VERSION 0.8

build:
    FROM docker:27-cli
    WITH DOCKER --isolate
        RUN docker info --format "{{.ServerVersion}}" > /out.txt
    END
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, nil)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, cache)

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("the build failed: %v\n%s", err, out.String())
	}

	got, err := os.ReadFile(dir + "/out.txt")
	if err != nil {
		t.Fatalf("the build reported success and saved nothing: %v\n%s", err, out.String())
	}

	// A version, not an empty line. `docker info --format` renders nothing and
	// exits zero against no server (E364), so an empty artefact is exactly what
	// a build that never started a daemon would produce - and it would have
	// looked like a pass.
	if strings.TrimSpace(string(got)) == "" {
		t.Errorf("the step reached no daemon; the artefact is empty, which is what"+
			" `docker info` prints when there is no server\n%s", out.String())
	}

	t.Logf("the step's daemon said: %s", strings.TrimSpace(string(got)))
}

// The same image and no WITH DOCKER block, which is the control.
//
// Added when the test above hung: with no step daemon running and no output, the
// question was whether the daemon work was at fault or whether this base image
// simply does not build here. One variable moves between the two tests, which is
// the difference between a bisection and a guess.
func TestTheSameImageWithoutADockerBlockRuns(t *testing.T) {
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	guest := buildGuestd(t)
	cache := storeDir(t)

	dir := project(t, `VERSION 0.8

build:
    FROM docker:27-cli
    RUN docker --version > /out.txt
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, nil)

	t.Setenv("EARTH_GUESTD", guest)
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, cache)

	var out bytes.Buffer

	err := cli.Run(context.Background(), cli.Options{
		Dir: dir, Target: testTarget, Out: &out, Platform: testPlatform(),
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			t.Skipf("docker hub rate limit: %v", err)
		}

		t.Fatalf("the control build failed: %v\n%s", err, out.String())
	}

	got, err := os.ReadFile(dir + "/out.txt")
	if err != nil {
		t.Fatalf("the control build saved nothing: %v\n%s", err, out.String())
	}

	t.Logf("the control said: %s", strings.TrimSpace(string(got)))
}
