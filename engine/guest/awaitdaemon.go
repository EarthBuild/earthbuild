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
const waitAtMost = 90 * time.Second

// awaitDaemon waits for a daemon to answer, and returns what it said.
//
// **An empty answer is not an answer.** `docker info` against a socket with
// nothing behind it renders the empty string and exits zero, so a wait that
// stops at the first non-error stops before the daemon has started: E364's first
// integration test passed in 0.35s having measured nothing at all. The version
// string is asked for precisely because it cannot be produced without a server.
//
// ask is given the context so a client that hangs is cut off with the wait
// rather than outliving it; every is how often to try, and the caller's deadline
// - not a constant here - is how long the wait lasts.
func awaitDaemon(
	ctx context.Context,
	ask func(context.Context) (string, error),
	every time.Duration,
) (string, error) {
	// A deadline of its own, because the caller's has none.
	//
	// The step's context is the build's, which runs until the build ends, so a
	// daemon that never answers made the guest wait for ever - a build that
	// printed nothing and could only be killed (E395). Every unit test here
	// supplied a deadline and therefore never saw it.
	//
	// Whichever comes first: a caller that *does* impose one - a cancelled build,
	// a step with a timeout - still wins.
	ctx, stop := context.WithTimeout(ctx, waitAtMost)
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
