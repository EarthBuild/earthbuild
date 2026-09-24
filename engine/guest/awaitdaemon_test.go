package guest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A daemon is waited for until it answers, not until its socket exists.
//
// The distinction is not pedantry: E364's first attempt at an integration test
// passed in 0.35s while measuring nothing, because `docker info --format
// '{{.Driver}}'` renders the empty string and exits zero when there is no server
// at all. A wait that accepts the first non-error is a wait that ends before the
// daemon has started - and the step's first command then fails against a socket
// that was reported ready.
func TestADaemonIsWaitedForUntilItAnswers(t *testing.T) {
	t.Parallel()

	tries := 0
	ask := func(context.Context) (string, error) {
		tries++
		if tries < 3 {
			return "", nil // exits zero, says nothing: no server yet
		}

		return "29.4.3 vfs", nil
	}

	got, err := awaitDaemon(t.Context(), ask, time.Millisecond)
	if err != nil {
		t.Fatalf("a daemon that answered on the third ask was not waited for: %v", err)
	}

	if got != "29.4.3 vfs" {
		t.Errorf("the wait returned %q, not what the daemon said", got)
	}

	if tries < 3 {
		t.Errorf("the wait stopped asking after %d tries", tries)
	}
}

// A socket that never answers is a failure that says so.
//
// The empty answer is the whole finding, so the message has to carry it: "the
// daemon did not answer" and "there is no socket" send an author to different
// places, and the failure class - *a query whose empty answer is
// indistinguishable from success* - is exactly the one that produced a green
// test measuring nothing.
func TestASocketThatNeverAnswersIsNamedAsSuch(t *testing.T) {
	t.Parallel()

	ctx, done := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer done()

	_, err := awaitDaemon(ctx, func(context.Context) (string, error) {
		return "", nil
	}, time.Millisecond)

	if err == nil {
		t.Fatal("a daemon that never answered was reported ready")
	}

	if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("the failure does not say the daemon was silent: %v", err)
	}
}

// What the daemon last complained about survives the wait.
//
// *A diagnostic discarded at each boundary*: a wait that returns only "timed
// out" throws away the one line saying why - here, that the client could not
// reach the socket at all, which is a different fault from a daemon that is
// merely slow.
func TestTheLastComplaintSurvivesTheWait(t *testing.T) {
	t.Parallel()

	ctx, done := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer done()

	_, err := awaitDaemon(ctx, func(context.Context) (string, error) {
		return "", errors.New("cannot connect to the Docker daemon at unix:///x")
	}, time.Millisecond)

	if err == nil || !strings.Contains(err.Error(), "unix:///x") {
		t.Errorf("the client's own complaint did not survive: %v", err)
	}
}

// A wait with no deadline of its own still ends.
//
// **Every test above supplies a deadline, and production does not.** The step's
// context is the build's, which has none, so a daemon that never answers made
// the guest wait for ever: the build printed nothing, started nothing, and was
// killed by the test harness at ten minutes with the goroutine sitting in this
// function (E395).
//
// *Failure class: a bound that only the tests supplied.* Every unit test passed,
// and each one passed because it had quietly provided the thing the caller was
// missing.
func TestAWaitWithNoDeadlineOfItsOwnStillEnds(t *testing.T) {
	t.Parallel()

	began := time.Now()

	done := make(chan error, 1)

	go func() {
		// context.Background(), deliberately: no deadline, no cancel, exactly
		// what a step gets. The cap is this test's rather than production's,
		// which is the only difference between the two - it is the *existence*
		// of a cap the caller did not supply that is on trial here, and waiting
		// out the real ninety seconds to see it demonstrated nothing the fifty
		// milliseconds below does not.
		_, err := awaitDaemonWithin(context.Background(), func(context.Context) (string, error) {
			return "", errors.New("connection refused")
		}, time.Millisecond, 50*time.Millisecond)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a daemon that never answered was reported ready")
		}

		// And it kept what it was told, because a bounded wait that says only
		// "timed out" has thrown away the one line that says why (E365).
		if !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("the daemon's last complaint did not survive: %v", err)
		}

	// Far longer than the cap above and still nothing like the production one:
	// what a failure here means is that no cap applied at all, and that is the
	// hang a build saw.
	case <-time.After(30 * time.Second):
		t.Fatalf("the wait has not returned after %v, and nothing else will stop"+
			" it: this is the hang a build saw", time.Since(began))
	}
}
