//go:build linux && integration

package guest_test

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// A *step* gets a daemon, at the path the step knows it by.
//
// Everything before this proved the pieces separately. This proves the wiring:
// a request carrying a `Daemon` goes through `execRequest`, past the mounts, and
// the body - confined, chrooted into its own root - finds a listening socket at
// `/var/run/docker.sock`.
//
// The body is a static Go binary built into the step's filesystem, because that
// filesystem is empty: no shell, nothing dynamic to link against. It is the
// condition a `FROM scratch` step is in, and the only kind of program that can
// run in it.
func TestAStepIsGivenADaemonAtItsOwnPath(t *testing.T) {
	_, err := osexec.LookPath("dockerd")
	if err != nil {
		t.Skipf("no dockerd on this machine: %v", err)
	}

	if !guest.NeedsIsolation(t) {
		return
	}

	root := stepRoot(t)

	// The daemon writes as root-in-its-namespace, so what it leaves behind is
	// not removable by the user this test runs as.
	t.Cleanup(func() { _ = osexec.Command("unshare", "-Ur", "rm", "-rf", root).Run() })

	// This binary, copied in, rather than one built with `go build`. The step's
	// root is empty - no shell, nothing dynamic to link against - and the test
	// binary is static and already re-executes itself for the daemon shim, so it
	// can be the prober too. That also removes a Go toolchain from what a CI
	// container needs (E387).
	self, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}

	err := os.WriteFile(filepath.Join(root, "prober"), self, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = h.Release() })

	step, err := c.RunStep(context.Background(), h, guest.Step{
		Argv: []string{"/prober", "--earthbuild-test-probe", "/var/run/docker.sock"},
		Daemon: &guest.Daemon{
			Root:   "/var/lib/earthbuild-docker",
			Socket: "/var/run/docker.sock",
		},
	}, nil)
	if err != nil {
		t.Fatalf("the step could not be run at all: %v", err)
	}

	if step.Exit != 0 {
		t.Fatalf("the step did not reach its daemon (exit %d):\n%s", step.Exit, step.Output)
	}

	if !strings.Contains(step.Output, "reached the daemon") {
		t.Errorf("the step ran but said something else:\n%s", step.Output)
	}
}
