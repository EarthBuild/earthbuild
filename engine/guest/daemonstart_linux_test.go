//go:build linux && integration

package guest

import (
	"context"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A daemon starts in a user namespace this engine could make, and answers.
//
// **The proof that `WITH DOCKER` is reachable at all.** Every flag in
// `daemonArgs` came from a daemon refusing to start on a real machine, and a
// list of flags that is never run is a list somebody will tidy (E364).
//
// Tagged `integration` because it takes a dockerd, a kernel that allows an
// unprivileged user namespace, and about fifteen seconds - none of which a unit
// suite should assume. Run it on a machine `rootlessReady` says yes to:
//
//	go test -tags integration ./engine/guest/ -run TestADaemonStartsInAUserNamespace
//
// `unshare` stands in for the sandbox here. The namespace a step gets is the
// same shape - `CLONE_NEWUSER|CLONE_NEWNS`, mapped to root (E105) - and using
// the tool means this test measures the daemon rather than the guest.
func TestADaemonStartsInAUserNamespace(t *testing.T) {
	// The readiness probe lives in `engine/exec`, which imports this package, so
	// this test asks the cheaper question directly: is there a dockerd at all.
	// What the probe adds - subuid ranges, the id-mapping helpers - is the
	// host's decision about whether to *offer* a daemon, and is tested there.
	_, err := osexec.LookPath("dockerd")
	if err != nil {
		t.Skipf("no dockerd on this machine: %v", err)
	}

	root := t.TempDir()
	sock := filepath.Join(root, "d.sock")

	// **Cleaned up from inside a namespace.** The daemon writes as root-in-the-
	// namespace, so what it leaves is not removable by the user the test runs
	// as, and `TempDir`'s own cleanup fails on it - reported as a failing test
	// that had already passed. Registered after `TempDir`, so it runs before.
	t.Cleanup(func() {
		_ = osexec.Command("unshare", "-Ur", "rm", "-rf", root).Run()
	})

	// A writable /run: the plugin manager makes a directory there before it
	// reads any flag, and the host's is not writable from inside (E364).
	script := "mount -t tmpfs none /run; exec dockerd " +
		strings.Join(daemonArgs(root, sock), " ")

	ctx, stop := context.WithTimeout(context.Background(), 90*time.Second)
	defer stop()

	cmd := osexec.CommandContext(ctx, "unshare",
		"-Ur", "--mount", "--pid", "--fork", "sh", "-c", script)

	var log strings.Builder

	cmd.Stdout, cmd.Stderr = &log, &log

	err = cmd.Start()
	if err != nil {
		t.Fatalf("%v", err)
	}

	defer func() { _ = cmd.Process.Kill() }()

	// Answering is the assertion. A daemon that started and cannot be spoken to
	// is a daemon a step cannot use.
	began := time.Now()

	for range 60 {
		// **Server version as well as driver.** `docker info` prints a client
		// section too, and a format that names only a server field renders
		// empty rather than failing when there is no server - so a test asking
		// for the driver alone can pass against nothing at all.
		out, err := osexec.CommandContext(ctx, "docker", "-H", "unix://"+sock,
			"info", "--format", "{{.ServerVersion}} {{.Driver}}").Output()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			got := strings.Fields(strings.TrimSpace(string(out)))

			t.Logf("a daemon answered in %v: %v", time.Since(began).Round(time.Millisecond), got)

			if len(got) != 2 {
				t.Fatalf("a daemon answered with %q, which names no server", out)
			}

			if got[1] != "vfs" {
				t.Errorf("the daemon is using %q, not the driver it was given",
					got[1])
			}

			return
		}

		time.Sleep(time.Second)
	}

	t.Fatalf("no daemon answered on %s within a minute\n%s", sock, log.String())
}
