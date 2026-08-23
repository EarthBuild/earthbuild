//go:build linux && integration

package guest

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"time"

	"golang.org/x/sys/unix"
)

// The whole lifetime, against a real dockerd.
//
// E364 started one by hand and proved the flags. This proves the code that will
// run in a step: make the directories, launch, wait until it *answers*, run a
// body that talks to it, and stop it - with the body asking the daemon a
// question only a running server can answer.
//
// Behind the `integration` tag because it needs a `dockerd` on the machine and
// several seconds; the unit tests around it use a stand-in and assert the
// ordering.
func TestTheWholeDaemonLifetimeAgainstARealDockerd(t *testing.T) {
	// A bind is the last thing this test needs and the first that can be
	// refused: the socket is published into the step with `mount --bind`, which
	// takes CAP_SYS_ADMIN. Asked rather than assumed, and asked *before* a
	// daemon is started, so an unprivileged run skips in milliseconds instead of
	// failing after a dockerd has come up (E160's rule, E415's occasion).
	if err := canBind(t); err != nil {
		t.Skipf("this machine cannot bind-mount, so a step's socket cannot be"+
			" published into it: %v", err)
	}

	root := t.TempDir()

	// The daemon writes as root inside its user namespace, so the ordinary
	// cleanup cannot remove what it left. Registered after TempDir's, therefore
	// running before it.
	t.Cleanup(func() { _ = osexec.Command("unshare", "-Ur", "rm", "-rf", root).Run() })

	d := &Daemon{Root: "/var/lib/earthbuild-docker", Socket: "/var/run/docker.sock"}

	ctx, done := context.WithTimeout(context.Background(), 90*time.Second)
	defer done()

	var said string

	began := time.Now()

	err := withDaemon(ctx, root, d, launchDockerd, publishSocket, func() error {
		_, sock := daemonPaths(root, d)

		out, err := osexec.Command("docker", "-H", "unix://"+sock,
			"info", "--format", "{{.ServerVersion}} {{.Driver}}").CombinedOutput()
		said = strings.TrimSpace(string(out))

		return err
	})
	if err != nil {
		t.Fatalf("the step's daemon did not see it through: %v\n  it said: %s", err, said)
	}

	if said == "" {
		t.Fatal("the body reached the daemon and it answered nothing, which is what" +
			" an unstarted daemon looks like (E364)")
	}

	t.Logf("a step's own daemon answered in %v: %s", time.Since(began).Round(time.Millisecond), said)

	// The storage is where it was told to put it, not where dockerd defaults to.
	// A daemon writing to the host's `/var/lib/docker` would work perfectly and
	// share everything with every other step on the machine (E362).
	if _, err := os.Stat(filepath.Join(root, "var/lib/earthbuild-docker/data")); err != nil {
		t.Errorf("the daemon did not store where it was told: %v", err)
	}
}

// canBind reports whether this process may bind-mount at all.
//
// A trial rather than a check on the uid: the conditions are the kernel's, and
// root inside a user namespace has the capability while root outside one may not
// have the mount namespace to use it in.
func canBind(t *testing.T) error {
	t.Helper()

	dir := t.TempDir()

	from, to := filepath.Join(dir, "from"), filepath.Join(dir, "to")

	for _, p := range []string{from, to} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			return err
		}
	}

	if err := unix.Mount(from, to, "", unix.MS_BIND, ""); err != nil {
		return err
	}

	_ = unix.Unmount(to, unix.MNT_DETACH)

	return nil
}
