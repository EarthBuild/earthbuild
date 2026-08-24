package cli_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/cli"
)

// An interrupt cancels the build context.
func TestAnInterruptCancelsTheBuild(t *testing.T) { //nolint:paralleltest // see the note above
	// Not parallel: it signals this process, and a sibling would see it too.
	ctx, stop := cli.InterruptContext(context.Background())
	defer stop()

	err := syscall.Kill(os.Getpid(), syscall.SIGINT)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("an interrupt did not cancel the build context")
	}
}

// And a second interrupt is not swallowed.
//
// This is the half that is usually got wrong. A handler that stays installed
// makes a build which ignores the first Ctrl-C unkillable by the second, and a
// wedged build is exactly when somebody presses it twice.
//
// In a child, because the claim is that the *process dies* - which cannot be
// asserted from inside the process it is about. The child installs the context,
// deliberately ignores the cancellation, and waits; the first signal cancels a
// context nobody is reading and the second should end it.
func TestASecondInterruptIsNotSwallowed(t *testing.T) {
	if os.Getenv("EARTH_TEST_INTERRUPT_CHILD") != "" {
		ctx, stop := cli.InterruptContext(context.Background())
		defer stop()

		_ = ctx

		// Ignoring the cancellation on purpose: a build that is wedged is what
		// the second Ctrl-C is for.
		select {}
	}

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot find this test binary to re-run it: %v", err)
	}

	// This binary.
	cmd := exec.CommandContext(t.Context(), self, "-test.run", "^TestASecondInterruptIsNotSwallowed$") //nolint:gosec
	cmd.Env = append(os.Environ(), "EARTH_TEST_INTERRUPT_CHILD=1")

	err = cmd.Start()
	if err != nil {
		t.Fatal(err)
	}

	// Long enough for the child to have installed its handler. Without this the
	// first signal arrives before there is anything to catch it and the child
	// dies of the first, which would pass for the wrong reason.
	time.Sleep(300 * time.Millisecond)

	for range 2 {
		_ = cmd.Process.Signal(syscall.SIGINT)
		time.Sleep(200 * time.Millisecond)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("the child ended with %v, not a signal", err)
		}

		st, ok := exit.Sys().(syscall.WaitStatus)
		if !ok || !st.Signaled() || st.Signal() != syscall.SIGINT {
			t.Errorf("the child ended as %v; the second interrupt should have"+
				" reached the operating system", exit)
		}

	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()

		t.Fatal("the child survived two interrupts, so the handler never stood" +
			" aside and a wedged build cannot be stopped")
	}
}
