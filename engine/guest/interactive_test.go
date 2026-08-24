package guest_test

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/nstest"
	"github.com/creack/pty"
)

// An interactive step runs on the caller's terminal.
//
// The pieces are in place - a descriptor channel (E189, E191) and a step that
// claims a terminal as its own (E190) - and this joins them: a step asked for
// with a terminal gets *that* terminal, across the protocol.
//
// The assertion is the controlling one, not `test -t 0`. A step whose streams
// point at a pty passes the easy check and has no job control, and the whole
// reason `RUN --interactive` needs a descriptor rather than a relay is the
// difference between those two.
func TestAnInteractiveStepRunsOnTheCallersTerminal(t *testing.T) { //nolint:paralleltest // see the note above
	// Inside a user namespace on Linux: the guest mounts /proc for every step,
	// confined or not, and an unprivileged process cannot. Not parallel, because
	// nstest re-executes this test on its own.
	if !nstest.In(t) {
		return
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}

	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })

	root := stepRoot(t)
	c := pairWithTerminals(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = h.Release() })

	lines := make(chan string, 8)

	go func() {
		sc := bufio.NewScanner(ptmx)
		for sc.Scan() {
			lines <- strings.TrimSpace(sc.Text())
		}

		close(lines)
	}()

	done := make(chan error, 1)

	go func() {
		_, runErr := c.RunStep(context.Background(), h, guest.Step{
			Argv:     []string{testShell, "-c", `test -t 0 && echo IS-TTY`},
			Terminal: tty,
		}, nil)
		done <- runErr
	}()

	select {
	case l := <-lines:
		// The step reads and writes the caller's terminal. It does not *own* it:
		// a terminal can be claimed by one session and it is already the
		// caller's, which E197 measured rather than assumed. So `isatty` is
		// true, a prompt works, `read` works - and job control stays with the
		// engine, where Ctrl-C cancels the build.
		if l != "IS-TTY" {
			t.Errorf("the step said %q; it was handed a terminal and cannot see one", l)
		}
	case <-time.After(15 * time.Second):
		// The step's own error, if it has one: a step that failed to start says
		// so here, and waiting for the terminal to speak would report the
		// silence rather than the cause.
		select {
		case err := <-done:
			t.Fatalf("the step never spoke on the terminal it was given: %v", err)
		default:
			t.Fatal("the step never spoke on the terminal it was given, and has not returned")
		}
	}

	err = <-done
	if err != nil {
		t.Errorf("the step failed: %v", err)
	}
}

// Two prompts cannot share one terminal, and the second is refused.
//
// There is one terminal and it belongs to whoever is typing. A second
// interactive step would take its input from the same descriptor - both reading,
// each getting some of the keystrokes - which is not a degraded session but a
// wrong one. Refused while another is running, by name.
func TestASecondInteractiveStepIsRefused(t *testing.T) { //nolint:paralleltest // see the note above
	// Inside a user namespace on Linux: the guest mounts /proc for every step,
	// confined or not, and an unprivileged process cannot. Not parallel, because
	// nstest re-executes this test on its own.
	if !nstest.In(t) {
		return
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty here: %v", err)
	}

	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })

	root := stepRoot(t)
	c := pairWithTerminals(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = h.Release() })

	held := make(chan struct{})

	firstErr := make(chan error, 1)

	go func() {
		defer close(held)

		_, runErr := c.RunStep(context.Background(), h, guest.Step{
			// Reads from the terminal rather than sleeping: `read` blocks on the
			// descriptor under test, so the hold cannot end early. `sleep 3`
			// stood here and the step returned in ten milliseconds - a non-zero
			// exit is a *result* rather than an error in this engine, so a
			// missing `sleep` freed the terminal and the second step was
			// allowed, which read exactly like the refusal not working.
			Argv:     []string{testShell, "-c", "echo FIRST; read x"},
			Terminal: tty,
		}, nil)
		firstErr <- runErr
	}()

	// Wait for the first to be on the terminal before asking for a second, and
	// give up rather than block: a scanner on a pty that never speaks waits for
	// as long as the suite is allowed to run.
	first := make(chan struct{})

	go func() {
		sc := bufio.NewScanner(ptmx)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "FIRST" {
				close(first)

				return
			}
		}
	}()

	select {
	case <-first:
	case <-time.After(15 * time.Second):
		t.Fatal("the first interactive step never reached the terminal")
	}

	_, err = c.RunStep(context.Background(), h, guest.Step{
		Argv:     []string{testShell, "-c", "echo SECOND"},
		Terminal: tty,
	}, nil)
	if err == nil {
		// What the first one did, because "two were allowed" and "the first
		// ended early and freed the terminal" look identical from here.
		select {
		case e := <-firstErr:
			t.Fatalf("two interactive steps were allowed on one terminal; the"+
				" first returned %v", e)
		default:
			t.Fatal("two interactive steps were allowed on one terminal, and the" +
				" first is still running")
		}
	}

	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("the refusal does not say what is in use: %v", err)
	}

	// Hang up, which is how an interactive session actually ends: the user's
	// terminal goes away and the step's `read` sees EOF.
	//
	// A newline stood here and did not reliably release it on macOS - the step
	// stayed in `read` and the test waited for it until the suite's own timeout,
	// which is a hang rather than a failure and reports nothing (E194).
	_ = ptmx.Close()

	select {
	case <-held:
	case <-time.After(15 * time.Second):
		t.Error("the first step did not end when its terminal was hung up")
	}
}

// pairWithTerminals is pairWith with a descriptor channel between the two.
func pairWithTerminals(t *testing.T, srv *guest.Server) *guest.Client {
	t.Helper()

	hostSide, guestSide, err := fdpass.SocketPair()
	if err != nil {
		t.Skipf("no socketpair: %v", err)
	}

	t.Cleanup(func() { _ = hostSide.Close(); _ = guestSide.Close() })

	hostFDs, guestFDs, err := fdpass.SocketPair()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = hostFDs.Close(); _ = guestFDs.Close() })

	srv.Terminals = guestFDs

	go func() { _ = srv.Serve(context.Background(), guestSide) }()

	c, err := guest.Dial(hostSide)
	if err != nil {
		t.Fatal(err)
	}

	c.Terminals = hostFDs

	_ = errors.New

	return c
}
