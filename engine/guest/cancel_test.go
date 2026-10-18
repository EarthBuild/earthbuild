package guest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// A cancelled step stops being waited for.
//
// `ExecStream` took a context and passed nil, so nothing about a build could be
// interrupted while a step was running: Ctrl-C during a five-minute compile was
// a five-minute wait, and a caller with a deadline did not get one.
//
// The wait is the part the caller owns, so it is the part asserted here.
func TestACancelledStepStopsWaiting(t *testing.T) {
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Parallel()

	root := stepRoot(t)
	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	start := time.Now()

	_, _, err = c.ExecStream(ctx, h, []string{sh(t), "-c", bin(t, "sleep") + " 30"}, nil, nil)

	took := time.Since(start)

	if err == nil {
		t.Fatal("a cancelled step reported success")
	}

	if !isCancellation(err) {
		t.Errorf("the failure does not say it was cancelled: %v", err)
	}

	// Generous: the point is seconds rather than half a minute.
	if took > 5*time.Second {
		t.Errorf("the caller waited %v for a step it had cancelled", took)
	}
}

// And the step itself stops.
//
// Returning early without killing anything would be the worse lie: the host
// stops tracking a step that is still writing into the handle it is about to
// release, so the build reports one thing and the sandbox does another.
//
// The command writes its marker *after* a sleep, so the marker's absence is the
// evidence the process was killed rather than left to finish.
func TestACancelledStepIsKilled(t *testing.T) {
	t.Parallel()

	root := stepRoot(t)
	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	marker := filepath.Join(t.TempDir(), "ran-anyway")

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	_, _, _ = c.ExecStream(ctx, h,
		[]string{sh(t), "-c", bin(t, "sleep") + " 2 && " + bin(t, "touch") + " " + marker}, nil, nil)

	// Past when the command would have written it, had it been left alone.
	time.Sleep(3 * time.Second)

	_, err = os.Stat(marker)
	if err == nil {
		t.Error("the cancelled step ran to completion, so nothing was killed")
	}
}

// A cancel for a step that has already finished is not an error.
//
// The host cannot know, when it decides to cancel, whether the step ended a
// moment earlier - so the race is ordinary and must be silent. Reporting it
// would make every cancellation near the end of a step look like a fault.
func TestCancellingAFinishedStepIsQuiet(t *testing.T) {
	t.Parallel()

	if !guest.NeedsIsolation(t) {
		return
	}

	root := stepRoot(t)
	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	// Runs and finishes.
	_, _, err = c.ExecStream(context.Background(), h, []string{sh(t), "-c", testTrue}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A context already cancelled when the next step starts: the wait is
	// abandoned immediately and a cancel goes out for a request the guest may
	// never have started. Nothing about that is an error, and the connection
	// has to survive it - the next step still has to run.
	dead, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = c.ExecStream(dead, h, []string{sh(t), "-c", testTrue}, nil, nil)
	if !isCancellation(err) {
		t.Errorf("a step on a cancelled context did not report cancellation: %v", err)
	}

	code, _, err := c.ExecStream(context.Background(), h, []string{sh(t), "-c", testTrue}, nil, nil)
	if err != nil {
		t.Fatalf("the connection did not survive a cancel: %v", err)
	}

	if code != 0 {
		t.Errorf("the step after a cancel exited %d", code)
	}
}

// isCancellation reports whether an error is the context's rather than a step's.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled")
}
