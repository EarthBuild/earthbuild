package cli_test

import (
	"bytes"
	"context"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// A build that has returned is a build that has stopped.
//
// Test-plan b5, written for the front end rather than the fleet it was drafted
// for, because the fleet does not exist yet and this is where the goroutines
// are today: a connection reader per sandbox, a prefetch that runs beside
// interpretation, and a warm-up that boots a VM on another goroutine. Each of
// those is a goroutine started by something with a `close` or a `defer`, and
// each is a place where the close can be forgotten.
//
// A leak here is not a crash. It is a process that keeps a VM connection open
// after the build using it is over, which matters most for the thing this
// engine is meant to become - a long-lived daemon serving many builds - and
// which nothing else in the suite would notice.
//
// Repeated on purpose. One build leaking one goroutine is inside the noise of a
// test binary; three builds leaking three is not, and the count is what makes a
// slow leak visible at all.
func TestABuildLeavesNoGoroutinesBehind(t *testing.T) { //nolint:paralleltest // boots a VM, see e2e_sandbox_test.go
	if os.Getenv("EARTH_TEST_NETWORK") == "" {
		t.Skip("set EARTH_TEST_NETWORK=1 to run tests that reach the internet")
	}

	requireSandbox(t)

	guest := buildGuestd(t)
	cache := storeDir(t)
	sh := testShell

	dir := project(t, `VERSION 0.8

build:
    FROM alpine:3.22
    RUN `+sh+` -c "echo leak-check > /out.txt"
    SAVE ARTIFACT /out.txt AS LOCAL out.txt
`, nil)

	run := func() {
		t.Helper()

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

			t.Fatalf("%v\n%s", err, out.String())
		}
	}

	// One build first, so the baseline includes whatever a build starts *once*
	// and keeps on purpose - a sandbox connection is reused by design, and
	// counting it as a leak would make this test a report of the design.
	run()

	before := settled()

	for range 3 {
		run()
	}

	after := settled()

	// A little slack: the runtime starts goroutines of its own, and a test
	// binary is not a quiet process. Three builds leaking one apiece is four
	// over, which this catches; one stray finaliser is not.
	const slack = 3

	if after > before+slack {
		t.Errorf("three builds left %d goroutines behind (%d -> %d)\n%s",
			after-before, before, after, debug.Stack())
	}
}

// settled is the goroutine count once it stops moving.
//
// A raw NumGoroutine is a reading of whatever the runtime was doing at that
// instant. Looping until two readings agree is what makes the number mean
// "nothing is still finishing" rather than "nothing had started yet".
func settled() int {
	var (
		prev  = -1
		count int
	)

	for range 50 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)

		count = runtime.NumGoroutine()
		if count == prev {
			break
		}

		prev = count
	}

	return count
}
