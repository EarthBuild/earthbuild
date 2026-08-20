//go:build linux && integration

package guest_test

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// A step reaches a daemon it did not start, through a mount.
//
// The nesting case end to end, and the one mechanism nothing had exercised on
// Linux: a `Sandbox` mount carrying a *socket* into a step. macOS has relied on
// it since WITH DOCKER landed, and a bind of a socket is not obviously the same
// thing as a bind of a file - the endpoint lives in the filesystem but the
// connection does not.
//
// The daemon here is a real one, started the way a step's own is started, and
// the step that reaches it is confined and chrooted with nothing in its root but
// the prober.
func TestAStepReachesADaemonItDidNotStart(t *testing.T) {
	if _, err := osexec.LookPath("dockerd"); err != nil {
		t.Skipf("no dockerd on this machine: %v", err)
	}

	if !guest.NeedsIsolation(t) {
		return
	}

	// Two roots: one for the daemon that is already running - standing in for an
	// outer step - and one for the step that inherits it.
	outer := t.TempDir()
	t.Cleanup(func() { _ = osexec.Command("unshare", "-Ur", "rm", "-rf", outer).Run() })

	inner := stepRoot(t)

	// This binary, copied in - see the note in daemonstep_linux_test.go (E387).
	self, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(inner, "prober"), self, 0o700); err != nil {
		t.Fatal(err)
	}

	d := &guest.Daemon{Root: "/var/lib/earthbuild-docker", Socket: "/var/run/docker.sock"}

	// Timed, because a passing test that finishes faster than a dockerd can
	// start has twice now been a test measuring the wrong thing (E364, E378).
	began := time.Now()

	err = guest.RunWithDaemonForTest(context.Background(), outer, d, func() error {
		// **Asserted, not logged.** A subtest's output is swallowed when it
		// passes, so a timing printed here proves nothing to anyone reading a
		// green run - and the two occasions this project mistook a fast pass for
		// a good one (E364, E378) were both caught by a number nobody had asked
		// for. So the body asks the daemon something only a running daemon can
		// answer, and refuses an empty reply.
		sock := filepath.Join(outer, "var/run/docker.sock")

		said, err := osexec.Command("docker", "-H", "unix://"+sock,
			"info", "--format", "{{.ServerVersion}}").CombinedOutput()
		if err != nil || strings.TrimSpace(string(said)) == "" {
			t.Fatalf("the outer daemon answered nothing after %v, so what the step"+
				" reaches below is not a daemon: %v %s",
				time.Since(began).Round(time.Millisecond), err, said)
		}

		c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: inner}})

		h, err := c.Materialise(context.Background(), nil)
		if err != nil {
			return err
		}

		defer func() { _ = h.Release() }()

		// The outer daemon's socket, bound into the inner step at the path its
		// client will look. This is what `withSocket` arranges for a block that
		// shares (E385).
		errStep, err := c.RunStep(context.Background(), h, guest.Step{
			Argv: []string{"/prober", "--earthbuild-test-probe", "/var/run/docker.sock"},
			Mounts: []guest.Mount{{
				Sandbox: filepath.Join(outer, "var/run/docker.sock"),
				Target:  "/var/run/docker.sock",
			}},
		}, nil)
		if err != nil {
			t.Fatalf("the step could not be run at all: %v", err)
		}

		if code != 0 || !strings.Contains(out, "reached the daemon") {
			t.Fatalf("the step did not reach the daemon it was given (exit %d):\n%s", code, out)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
}
