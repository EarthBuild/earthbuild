package guest

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
)

// TestMain lets the test binary be a daemon shim.
//
// The launch re-executes this binary (E373); without this the child parses the
// shim's argv as test flags, exits at once, and every assertion about a running
// daemon passes while measuring an absence (E374).
func TestMain(m *testing.M) {
	RunDaemonShimIfAsked()
	RunStepShimIfAsked()
	runProbeIfAsked()
	runResolveIfAsked()

	os.Exit(m.Run())
}

// probeFlag makes this binary a socket prober rather than a test run.
const probeFlag = "--earthbuild-test-probe"

// runProbeIfAsked dials a socket and says whether it got there.
//
// The integration tests need a program that can run inside a step whose root is
// empty: no shell, no coreutils, nothing to link against. They used to build one
// with `go build`, which works on a developer machine and rules out every CI
// container that has no Go toolchain - and this binary is already static and
// already re-executes itself for the daemon shim, so it can be the prober too.
//
// The same trick the shim uses, for the same reason: the only executable
// guaranteed to be available is the one already running.
func runProbeIfAsked() {
	if len(os.Args) < 3 || os.Args[1] != probeFlag {
		return
	}

	c, err := (&net.Dialer{}).DialContext(context.Background(), "unix", os.Args[2]) //nolint:gosec // arguments this test wrote
	if err != nil {
		fmt.Println("no daemon at", os.Args[2]+":", err)
		os.Exit(1)
	}

	_ = c.Close()

	fmt.Println("reached the daemon")
	os.Exit(0)
}

// resolveFlag makes this binary resolve a name and print what it got.
const resolveFlag = "--earthbuild-test-resolve"

// runResolveIfAsked answers what a name resolves to, from inside a step.
//
// The same re-execution trick as the socket prober: a step's root is empty, and
// the only executable guaranteed to be there is the one already running. Go's
// resolver reads `/etc/hosts` itself, which is exactly the file under test.
func runResolveIfAsked() {
	if len(os.Args) < 3 || os.Args[1] != resolveFlag {
		return
	}

	addrs, err := (&net.Resolver{}).LookupHost(context.Background(), os.Args[2]) //nolint:gosec // arguments this test wrote
	if err != nil {
		fmt.Println("cannot resolve", os.Args[2]+":", err)
		os.Exit(1)
	}

	fmt.Println(strings.Join(addrs, " "))
	os.Exit(0)
}
