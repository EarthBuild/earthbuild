package guest

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// waitAtMost is how long a daemon is given to answer before the step is failed.
//
// Generous against the measurement: a daemon answers in about 1.4 seconds on the
// machine this was built on (E375), and this is sixty times that. It is not a
// performance budget - it is the difference between a build that fails with a
// reason and a build that hangs (E395).
// waitAtMost is how long one attempt waits for a daemon to answer.
//
// **45s and two attempts, not 90s and one.** A dockerd that has not answered in
// 45 seconds is usually dead rather than slow, and the second attempt relaunches
// it - which rescues a build that a single long wait only delays. The worst case
// is unchanged at 90 seconds; what changed is that half of it is spent on a
// fresh daemon rather than on the same one.
//
// A var rather than a const so a test can shorten it. Nothing else writes it.
var waitAtMost = 45 * time.Second

// awaitDaemon waits for a daemon to answer, and returns what it said.
//
// **An empty answer is not an answer.** `docker info` against a socket with
// nothing behind it renders the empty string and exits zero, so a wait that
// stops at the first non-error stops before the daemon has started: E364's first
// integration test passed in 0.35s having measured nothing at all. The version
// string is asked for precisely because it cannot be produced without a server.
//
// ask is given the context so a client that hangs is cut off with the wait
// rather than outliving it; every is how often to try.
func awaitDaemon(
	ctx context.Context,
	ask func(context.Context) (string, error),
	every time.Duration,
) (string, error) {
	return awaitDaemonWithin(ctx, ask, every, waitAtMost)
}

// awaitDaemonWithin is awaitDaemon with the cap named rather than assumed.
//
// The cap is a parameter so that the test which proves the cap exists does not
// have to wait ninety seconds to prove it. What is under test is that this
// function bounds its own wait when the caller has not - and a wait that ends
// after fifty milliseconds demonstrates that exactly as well as one that ends
// after ninety seconds, in a package whose whole run was ninety-five.
func awaitDaemonWithin(
	ctx context.Context,
	ask func(context.Context) (string, error),
	every, atMost time.Duration,
) (string, error) {
	// A deadline of its own, because the caller's may have none.
	//
	// The step's context is the build's, which runs until the build ends, so a
	// daemon that never answers made the guest wait for ever - a build that
	// printed nothing and could only be killed (E395). Every unit test here
	// supplied a deadline and therefore never saw it.
	//
	// Whichever comes first: a caller that *does* impose one - a cancelled build,
	// a step with a timeout - still wins.
	ctx, stop := context.WithTimeout(ctx, atMost)
	defer stop()

	var last error

	for {
		said, err := ask(ctx)

		switch {
		case err == nil && strings.TrimSpace(said) != "":
			return said, nil

		case err != nil:
			// Kept rather than counted: the client's own line says whether the
			// socket is absent, refusing connections, or answering slowly, and a
			// wait that reports only "timed out" throws that away.
			last = err
		}

		select {
		case <-ctx.Done():
			if last != nil {
				return "", fmt.Errorf(
					"the daemon did not answer before the deadline; it last said: %w", last)
			}

			return "", fmt.Errorf(
				"the daemon did not answer before the deadline: the socket accepted the"+
					" question and returned nothing, which is what an unstarted daemon"+
					" looks like (%w)", ctx.Err())

		case <-time.After(every):
		}
	}
}
