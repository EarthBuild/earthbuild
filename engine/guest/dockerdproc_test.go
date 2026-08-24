package guest

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A guest with no dockerd says so, and says whose problem it is.
//
// The daemon is the *guest's*, not the image's (E368), so "install docker in
// your base image" is exactly the wrong advice and is what every message about a
// missing daemon says by default. The refusal has to name the machine.
func TestAGuestWithNoDockerdSaysSo(t *testing.T) {
	t.Parallel()

	_, err := launchWith(t.Context(),
		func(string) (string, error) { return "", errors.New("not in $PATH") },
		[]string{"--host=unix:///x"}, "/x")

	if err == nil {
		t.Fatal("a guest with no dockerd launched one anyway")
	}

	for _, want := range []string{"dockerd", "guest"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// Stopping a daemon stops it.
//
// A process that ignores SIGTERM - and `dockerd` shutting down a container will
// take its time - must still be gone when the step's handle is released, because
// what it holds open is the filesystem the capture is about to read.
func TestStoppingADaemonStopsIt(t *testing.T) {
	t.Parallel()

	// A stand-in that ignores SIGTERM, which is the case worth testing: a
	// well-behaved process would exit on the first signal and prove nothing
	// about the second.
	script := filepath.Join(t.TempDir(), "stubborn")
	err := os.WriteFile(script, []byte("#!/bin/sh\ntrap '' TERM\nwhile :; do sleep 1; done\n"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	proc, err := launchWith(t.Context(),
		func(string) (string, error) { return script, nil }, nil, "/unused.sock")
	skipIfUnprivileged(t, err)

	d, ok := proc.(*dockerd)
	if !ok {
		t.Fatalf("launch returned a %T", proc)
	}

	pid := d.cmd.Process.Pid

	// It is actually running before it is stopped.
	//
	// Without this the test survives a launch that starts nothing: a child that
	// exited immediately is stopped trivially, and every assertion below passes
	// while measuring an absence. That is not hypothetical - it is what happened
	// the moment the launch began re-executing this binary through the shim
	// (E374).
	for range 200 {
		if _, statErr := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); statErr == nil {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	err = syscall.Kill(pid, 0)
	if err != nil {
		t.Fatalf("nothing was running to stop: %v", err)
	}

	err = proc.Stop()
	if err != nil {
		t.Fatalf("a stubborn daemon would not stop: %v", err)
	}

	// Signal 0 asks whether it is there without touching it. A reaped child is
	// gone from this process's point of view, which is the point of view that
	// matters: nothing of ours is holding the step's filesystem.
	err = syscall.Kill(pid, 0)
	if err == nil {
		t.Errorf("pid %d is still running after Stop", pid)
	}
}

// A daemon that exits on its own is not waited on forever.
//
// The wait's deadline is the caller's, but a process that has already died says
// so immediately rather than after it - otherwise a `dockerd` that refuses its
// own flags costs every WITH DOCKER step the full timeout before the author is
// told anything.
func TestADaemonThatDiedIsNoticed(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "quitter")
	err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	proc, err := launchWith(t.Context(),
		func(string) (string, error) { return script, nil }, nil, "/unused.sock")
	skipIfUnprivileged(t, err)

	// Generous, because it is a *ceiling* and not a measurement: the loop returns
	// as soon as it notices, so a longer budget costs nothing in the ordinary
	// case and stops the test failing when the machine is running the whole
	// repository's suites at once. Two seconds was enough alone and not enough
	// under `go test ./...` - the contention-window flake this project has met
	// before (E336).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, err := proc.Ask(t.Context())
		if err != nil && strings.Contains(err.Error(), "exited") {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Error("a daemon that exited was still being asked whether it was up")
}

// Stopping a daemon that has already been noticed to have died still returns.
//
// The exit arrives once. If the code that notices it - `Ask`, on every poll -
// consumes the same signal the shutdown waits on, then a daemon that failed on
// its own flags hangs the step forever at the very point the engine was trying
// to report the failure.
//
// *Failure class: a one-shot signal read from two places.* The test is written
// with its own deadline because the symptom is a hang, and a hung test is
// indistinguishable from a slow one until the suite times out.
func TestStoppingAnAlreadyNoticedDeadDaemonReturns(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "quitter")
	err := os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	proc, err := launchWith(t.Context(),
		func(string) (string, error) { return script, nil }, nil, "/unused.sock")
	skipIfUnprivileged(t, err)

	// Notice the death first, which is what the wait does on every poll.
	// Same ceiling, same reason.
	for range 3000 {
		_, err := proc.Ask(t.Context())
		if err != nil && strings.Contains(err.Error(), "exited") {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	returned := make(chan error, 1)
	began := time.Now()

	go func() { returned <- proc.Stop() }()

	select {
	case err := <-returned:
		// No error. The daemon dying is the step's news and it has already been
		// reported by the wait; calling that a *shutdown* failure buries the
		// real one under "kill the step's daemon: no such process".
		if err != nil {
			t.Errorf("stopping an already-dead daemon was reported as a failure: %v", err)
		}

		// And promptly. Waiting out the grace period for a process that is
		// already gone costs every failed WITH DOCKER step that delay, and the
		// clock is the only thing that says so - the same tell as E364.
		if took := time.Since(began); took > gracePeriod/2 {
			t.Errorf("stopping a dead daemon took %v, most of the %v grace period"+
				" it did not need", took, gracePeriod)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never returned: the exit was consumed by the code that noticed it")
	}
}

// A daemon is asked on its own socket, not on whatever the client defaults to.
//
// `docker` with no `-H` and no `DOCKER_HOST` talks to `/var/run/docker.sock` -
// the *machine's* daemon. A launch that forgets to tell the process which socket
// it owns therefore asks a daemon that is already running, gets a version
// straight back, and reports the step's daemon ready before it has bound
// anything.
//
// The tell was the clock: the step-level test passed in 0.27s when starting a
// dockerd had been measured at 1.36s (E375). Three times now the timing has been
// the only thing that disagreed.
func TestADaemonIsAskedOnItsOwnSocket(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "sleeper")
	err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	proc, err := launchWith(t.Context(),
		func(string) (string, error) { return script, nil }, nil, "/steps/h1/var/run/docker.sock")
	skipIfUnprivileged(t, err)

	t.Cleanup(func() { _ = proc.Stop() })

	d, ok := proc.(*dockerd)
	if !ok {
		t.Fatalf("launch returned a %T", proc)
	}

	if d.sock != "/steps/h1/var/run/docker.sock" {
		t.Errorf("the daemon was launched not knowing its own socket: %q -"+
			" it would be asked on the machine's default one", d.sock)
	}
}

// What the daemon said before it died reaches the person who has to fix it.
//
// A daemon that will not start says why - `dockerd needs to be started with root
// privileges`, `mkdir /run/docker/plugins: permission denied`, `unix socket path
// too long`. Every one of those was a real failure in this project, and every one
// of them is the answer. Written to the guest's stderr they land in a log nobody
// is reading; what the author gets is `exit status 1`.
//
// *A diagnostic discarded at each boundary.* Especially for nesting: an inner
// build in a container without CAP_SYS_ADMIN cannot mount its private `/run`,
// and "exit status 1" is an unanswerable bug report.
func TestWhatTheDaemonSaidBeforeItDiedReachesTheAuthor(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "complainer")
	err := os.WriteFile(script,
		[]byte("#!/bin/sh\necho 'mkdir /run/docker/plugins: permission denied' >&2\nexit 1\n"),
		0o700)
	if err != nil {
		t.Fatal(err)
	}

	proc, err := launchWith(t.Context(),
		func(string) (string, error) { return script, nil }, nil, "/unused.sock")
	skipIfUnprivileged(t, err)

	t.Cleanup(func() { _ = proc.Stop() })

	var said error

	for range 3000 {
		_, e := proc.Ask(t.Context())
		if e != nil && strings.Contains(e.Error(), "exited") {
			said = e

			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	if said == nil {
		t.Fatal("the daemon exited and the wait never noticed")
	}

	if !strings.Contains(said.Error(), "/run/docker/plugins") {
		t.Errorf("the daemon's own complaint did not reach the caller:\n  %v", said)
	}
}

// skipIfUnprivileged skips when starting the stub needed a privilege this
// machine will not give.
//
// **The message was already right and the outcome was not.** These tests start a
// process in a mount namespace with a private `/run`, both of which need
// `CAP_SYS_ADMIN`; on a hosted runner AppArmor refuses the unprivileged user
// namespace that would hold them (E596) and `fork/exec` returns "operation not
// permitted". The test knew - it prints the requirement - and then failed,
// which reports a defect where there is a restriction.
//
// Everything else in this repository that needs that privilege skips and says
// so; `nstest.In` is the same sentence for the same reason (E606).
func skipIfUnprivileged(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		return
	}

	if errors.Is(err, syscall.EPERM) || strings.Contains(err.Error(), "operation not permitted") {
		t.Skipf("this machine will not start a daemon in a namespace of its own,"+
			" so nothing ran: %v", err)
	}

	t.Fatal(err)
}
