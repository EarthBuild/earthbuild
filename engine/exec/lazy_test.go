package exec_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/exec"
)

// An executor that is never asked to run anything never starts its sandbox.
//
// Measured, not supposed: a no-op rebuild of `FROM alpine + RUN true` - every
// step an L1 hit, nothing executed - took 790ms against 10ms for the same build
// with no sandbox in its plan. The difference was a VM booted for a build that
// ran nothing. Starting on first use rather than at construction is worth the
// whole of that, and it is the most common thing a developer does: build again
// after changing nothing.
func TestAnUnusedSandboxIsNeverStarted(t *testing.T) {
	t.Parallel()

	sb := &countingSandbox{confines: true, store: t.TempDir()}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	_ = e.Close()

	if boots, _ := sb.counts(); boots != 0 {
		t.Errorf("the sandbox was started %d times for a build that ran nothing", boots)
	}
}

// A sandbox that was never started is not stopped either.
//
// Stopping something that never began is not harmless: the backend is a CLI
// that reports an unknown container as an error, so a shutdown of nothing turns
// a clean build into one that prints a failure at the end.
func TestAnUnusedSandboxIsNeverStopped(t *testing.T) {
	t.Parallel()

	sb := &countingSandbox{confines: true, store: t.TempDir()}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	_ = e.Close()

	if _, stops := sb.counts(); stops != 0 {
		t.Errorf("a sandbox that never started was stopped %d times", stops)
	}
}

// The first step that needs the guest starts it, once, however many follow.
func TestTheFirstStepStartsTheSandboxExactlyOnce(t *testing.T) {
	t.Parallel()

	sb := &countingSandbox{confines: true, store: t.TempDir()}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	for range 3 {
		// The loopback guest answers nothing useful, so the error is expected
		// and irrelevant: what is under test is how many times the sandbox was
		// asked to start.
		_ = e.Ping(context.Background())
	}

	if boots, _ := sb.counts(); boots != 1 {
		t.Errorf("three steps started the sandbox %d times, want 1", boots)
	}
}

// A sandbox that cannot start says so at the step that needed it.
//
// Deferred rather than lost: a build whose every step is cached is entitled to
// succeed on a machine whose VM backend is broken, and one that must run
// something is entitled to a diagnosis naming the failure.
func TestAFailureToStartIsReportedWhenItMatters(t *testing.T) {
	t.Parallel()

	sb := &countingSandbox{confines: true, store: t.TempDir(), fail: context.DeadlineExceeded}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatalf("constructing an executor tried to start the sandbox: %v", err)
	}

	defer e.Close()

	err = e.Ping(context.Background())
	if err == nil {
		t.Error("a step ran against a sandbox that could not start")
	}
}

// deadFirst yields a sandbox whose first connection is a machine that is not
// there, and whose second is a working one.
type deadFirst struct {
	mu      sync.Mutex
	starts  int
	removes int
	store   string
}

func (d *deadFirst) Confines() bool   { return true }
func (d *deadFirst) StoreDir() string { return d.store }
func (d *deadFirst) Stop() error      { return nil }

func (d *deadFirst) Remove() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.removes++

	return nil
}

func (d *deadFirst) Start(context.Context) (exec.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.starts++

	if d.starts == 1 {
		// A connection to nothing: the container listing said this VM was
		// running, and it was not.
		return exec.ClosedConn(), nil
	}

	//nolint:contextcheck // see LoopbackConn: the connection's lifetime, not a request's
	return exec.LoopbackConn(), nil
}

func (d *deadFirst) counts() (int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.starts, d.removes
}

// A VM the listing calls running but which does not answer is removed and
// rebooted, once.
//
// This is the safety that a `container exec <name> true` probe used to provide,
// moved to where it costs nothing: the probe ran on every build at 50-70ms, and
// this runs only when the assumption it was checking turns out to be wrong. A
// listing can be stale, and a machine that is up but wedged answers `ls` and
// not a handshake - so without this a developer would be stuck until they knew
// to run a cleanup command.
func TestAWedgedSandboxIsRemovedAndRebooted(t *testing.T) {
	t.Parallel()

	sb := &deadFirst{store: t.TempDir()}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	err = e.Ping(context.Background())
	if err != nil {
		t.Fatalf("the build did not recover from a sandbox that was not there: %v", err)
	}

	starts, removes := sb.counts()
	if starts != 2 {
		t.Errorf("the sandbox was started %d times, want 2", starts)
	}

	if removes != 1 {
		t.Errorf("the dead VM was removed %d times, want 1", removes)
	}
}

// It is one retry, not a loop: a backend that is genuinely broken must say so
// rather than reboot forever.
func TestARecoveryIsAttemptedOnlyOnce(t *testing.T) {
	t.Parallel()

	sb := &alwaysDead{store: t.TempDir()}

	e, err := exec.New(sb)
	if err != nil {
		t.Fatal(err)
	}

	defer e.Close()

	err = e.Ping(context.Background())
	if err == nil {
		t.Fatal("a sandbox that never answers reported success")
	}

	if starts := sb.startCount(); starts != 2 {
		t.Errorf("the sandbox was started %d times, want 2", starts)
	}
}

type alwaysDead struct {
	mu     sync.Mutex
	starts int
	store  string
}

func (d *alwaysDead) Confines() bool   { return true }
func (d *alwaysDead) StoreDir() string { return d.store }
func (d *alwaysDead) Stop() error      { return nil }
func (d *alwaysDead) Remove() error    { return nil }

func (d *alwaysDead) Start(context.Context) (exec.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.starts++

	return exec.ClosedConn(), nil
}

func (d *alwaysDead) startCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.starts
}
