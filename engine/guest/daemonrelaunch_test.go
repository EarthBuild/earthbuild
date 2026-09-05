package guest

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A daemon that never comes up is launched again.
//
// **The failure this exists for is a dockerd that dies at startup.** Before
// this, `withDaemon` launched once, waited, and failed the step - so a daemon
// that would have come up on a second try took the build with it. `container
// run` already worked this way for the sandbox VM (run, fail, remove, run); the
// step's daemon did not.
//
//nolint:paralleltest // shortens waitAtMost, which the package shares
func TestADaemonThatDoesNotComeUpIsLaunchedAgain(t *testing.T) {
	// Not parallel: it shortens the package's wait, which is shared state.
	defer shortenTheWait(t)()

	launches := 0
	made := []*fakeDaemon{}

	launch := func(context.Context, []string, string) (daemonProcess, error) {
		launches++

		d := &fakeDaemon{says: "ok"}
		// The second launch is the one that answers, which is the whole point.
		if launches < 2 {
			d.err = errors.New("not up yet")
		}

		made = append(made, d)

		return d, nil
	}

	publish := func(_, _ string) (func(), error) { return func() {}, nil }

	ran := false
	err := withDaemon(context.Background(), t.TempDir(), &Daemon{Root: t.TempDir(), Socket: "d.sock"}, false,
		launch, publish, func() error {
			ran = true

			return nil
		})
	if err != nil {
		t.Fatalf("the second launch answers, so this should succeed: %v", err)
	}

	if launches != 2 {
		t.Errorf("launched %d times, want 2", launches)
	}

	if !ran {
		t.Error("the body never ran")
	}

	// One stop for the daemon that never answered, one for the one that did.
	// A relaunch that leaves the first process alive holds the socket the
	// second one binds.
	stopped := 0
	for _, d := range made {
		stopped += d.stopped
	}

	if stopped != 2 {
		t.Errorf("stopped %d daemons, want 2 - a failed attempt must not be left running", stopped)
	}
}

// A daemon that never comes up at all still fails, and is not left running.
//
//nolint:paralleltest // shortens waitAtMost, which the package shares
func TestADaemonThatNeverComesUpIsStoppedAndReported(t *testing.T) {
	defer shortenTheWait(t)()

	launches := 0
	made := []*fakeDaemon{}

	launch := func(context.Context, []string, string) (daemonProcess, error) {
		launches++

		d := &fakeDaemon{err: errors.New("not up yet")}
		made = append(made, d)

		return d, nil
	}

	publish := func(_, _ string) (func(), error) { return func() {}, nil }

	err := withDaemon(context.Background(), t.TempDir(), &Daemon{Root: t.TempDir(), Socket: "d.sock"}, false,
		launch, publish, func() error { return nil })
	if err == nil {
		t.Fatal("want an error when no daemon ever answers")
	}

	if !strings.Contains(err.Error(), "did not get one") {
		t.Errorf("the message should still say the step asked for a daemon, got: %v", err)
	}

	stopped := 0
	for _, d := range made {
		stopped += d.stopped
	}

	if launches != stopped {
		t.Errorf("launched %d and stopped %d - every launch must be stopped", launches, stopped)
	}
}

// A machine with no dockerd is told once, not twice.
//
// Retrying a missing binary spends the policy re-reading PATH and reports
// "failed after 2 attempts" about a fact that will not change.
func TestAMissingDockerdIsNotRetried(t *testing.T) {
	t.Parallel()

	launches := 0
	launch := func(context.Context, []string, string) (daemonProcess, error) {
		launches++

		return nil, exec.ErrNotFound
	}

	publish := func(_, _ string) (func(), error) { return func() {}, nil }

	err := withDaemon(context.Background(), t.TempDir(), &Daemon{Root: t.TempDir(), Socket: "d.sock"}, false,
		launch, publish, func() error { return nil })
	if err == nil {
		t.Fatal("want an error")
	}

	if launches != 1 {
		t.Errorf("launched %d times, want 1 - a missing binary is not a transient fault", launches)
	}
}

// shortenTheWait makes a failed await take milliseconds instead of 45 seconds.
//
// The timeout is the thing under test in one sense and pure cost in another:
// what these tests check is that a failure is *retried*, not how long the engine
// is willing to wait before calling it one.
func shortenTheWait(t *testing.T) func() {
	t.Helper()

	was := waitAtMost
	waitAtMost = 20 * time.Millisecond

	return func() { waitAtMost = was }
}
