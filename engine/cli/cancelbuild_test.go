package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A cancelled build returns, and returns quickly.
//
// E56 made the *guest* seam cancellable and proved it there. That is not the
// promise a person cares about: what they press Ctrl-C on is a build, and
// between their context and the step there is a scheduler running several
// things at once, an executor, and a protocol. Any one of those can hold the
// context and not pass it on - which is exactly what `ExecStream` did for
// months while every signature on the path took one.
//
// So this asserts the end of the chain rather than a link in it.
func TestACancelledBuildReturnsPromptly(t *testing.T) { // not parallel: boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	sh := testShell

	dir := project(t, `VERSION 0.8

slow:
    FROM alpine:3.22
    RUN `+sh+` -c "echo started > /marker.txt && sleep 120"
    SAVE ARTIFACT /marker.txt AS LOCAL marker.txt
`, nil)

	t.Setenv("EARTH_GUESTD", buildGuestd(t))
	t.Setenv("EARTH_IMAGE_CACHE_DIR", sharedImages(t))
	useStore(t, storeDir(t))

	ctx, cancel := context.WithCancel(context.Background())

	// Late enough that the step is running rather than the image still pulling:
	// cancelling before the step starts would pass without testing anything.
	go func() {
		time.Sleep(8 * time.Second)
		cancel()
	}()

	var out bytes.Buffer

	start := time.Now()

	err := cli.Run(ctx, cli.Options{
		Dir: dir, Target: "slow", Out: &out, Platform: testPlatform(),
	})

	took := time.Since(start)

	if err == nil {
		t.Fatal("a cancelled build reported success")
	}

	if strings.Contains(err.Error(), "429") {
		t.Skipf("docker hub rate limit: %v", err)
	}

	// The step sleeps for two minutes. Anything near that is the build waiting
	// it out; the margin is wide because a cold run has an image to pull first.
	if took > 60*time.Second {
		t.Errorf("the build took %v to return after being cancelled", took)
	}

	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("the failure does not say it was cancelled:\n%v", err)
	}
}
