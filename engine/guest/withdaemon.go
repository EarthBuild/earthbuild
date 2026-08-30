package guest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/EarthBuild/earthbuild/internal/retry"
)

// daemonProcess is a daemon that has been launched but is not yet known to be
// up.
//
// Two methods rather than one, because "started" and "answering" are different
// facts and conflating them is the E364 mistake: a process exists long before it
// serves, and the only proof of the second is asking it something only a server
// can answer.
type daemonProcess interface {
	// Ask puts a question to it, returning what it said.
	Ask(ctx context.Context) (string, error)
	// Stop ends it. Called exactly once, on every path.
	Stop() error
}

// launchDaemon starts a daemon process with the given argv.
type launchDaemon func(ctx context.Context, argv []string, sock string) (daemonProcess, error)

// howOftenToAsk is the gap between attempts while waiting for a daemon.
//
// Short, because the wait is on the critical path of every WITH DOCKER step and
// a daemon that is ready is ready within a second (E364 measured 1.076s); the
// cost of asking too often is a few `docker info` invocations against a socket
// that is not there yet.
const howOftenToAsk = 100 * time.Millisecond

// withDaemon runs body with a daemon of the step's own, and without one if it
// cannot get there.
//
// The order is the content:
//
//  1. make the directories, because `dockerd` creates neither the one it listens
//     in nor the one it stores in, and a thin base image has neither;
//
// The mounts the executor asked for are already in place when this runs, and in
// *this* process's mount namespace: `bindMounts` is called by the guest before
// the step starts, not by the step after unsharing. So a daemon running beside
// the step writes through the cache bind exactly as the step would - which is
// what makes `--cache-id` mean anything at all.
//  2. launch;
//  3. **wait until it answers**, not until it exists;
//  4. run the body;
//  5. stop it - on every path, including the one where the wait failed.
//
// Step 5 is the one worth a test. Returning early from a failed wait is the
// natural thing to write and leaves a `dockerd` running against a step that has
// been abandoned, holding its overlay open while the capture takes a layer of a
// filesystem still being written to.
// publishDaemon makes a running daemon's socket appear inside the step.
type publishDaemon func(from, to string) (func(), error)

// daemonPolicy is how a daemon that does not come up is retried.
//
// Two attempts, because each costs a full `waitAtMost` and the point is to
// relaunch once rather than to keep trying. A missing `dockerd` is declined:
// re-reading PATH cannot find a binary that is not installed, and reporting
// "failed after 2 attempts" about it would bury the one sentence that helps.
func daemonPolicy() retry.Policy {
	return retry.Policy{
		Attempts: 2,
		// Immediately, near enough: the wait already spent 45 seconds and the
		// interesting work is the relaunch, not the pause before it.
		Base:     100 * time.Millisecond,
		Strategy: retry.Fixed,
		Retryable: func(err error) bool {
			return !errors.Is(err, exec.ErrNotFound)
		},
	}
}

func withDaemon(
	ctx context.Context, stepRoot string, d *Daemon,
	launch launchDaemon, publish publishDaemon, body func() error,
) (out error) {
	root, inStep := daemonPaths(stepRoot, d)

	// The daemon listens on a short path of the guest's own and the socket is
	// bound into the step once it exists (E396). It cannot listen inside the
	// step: a store path plus a handle plus an overlay is past the 104 bytes a
	// sockaddr allows before `/var/run/docker.sock` is appended.
	listen, removeListenDir, err := shortSocket()
	if err != nil {
		return err
	}

	defer removeListenDir()

	// The storage. A build sees it, so it gets the mode a build is judged on
	// rather than one this engine prefers.
	//
	// The directory the socket appears in is made by `publish`, because it is
	// made *after* the daemon is up rather than before it starts.
	err = os.MkdirAll(root, 0o755) //nolint:gosec // a mode a build sees
	if err != nil {
		return fmt.Errorf("make %s for the step's daemon: %w", root, err)
	}

	// The guest's paths, not the step's. The daemon is not chrooted (E368), so
	// handing it `/var/run/docker.sock` would point it at the *guest's* one -
	// where it would either be refused or, far worse, succeed and serve every
	// step at once out of one storage area on the host.
	//
	// The two sets name the same files, which is exactly the condition under
	// which a mix-up survives review.
	// **Launched and waited for together, so a failure can be retried as one.**
	// A daemon that dies at startup used to fail the step: the wait timed out
	// and nothing relaunched it. `container run` has worked this way for the
	// sandbox VM for a long time - run, fail, remove, run - and this is the same
	// shape for the step's own daemon.
	//
	// The stop inside the attempt is load-bearing. The deferred one below
	// belongs to the daemon that answered; a daemon that never did still holds
	// the socket the next launch binds, so it has to go before the retry.
	var proc daemonProcess

	err = retry.Do(ctx, daemonPolicy(), func() error {
		started, launchErr := launch(ctx, daemonArgs(root, listen), listen)
		if launchErr != nil {
			return fmt.Errorf("start a daemon for this step: %w", launchErr)
		}

		_, awaitErr := awaitDaemon(ctx, started.Ask, howOftenToAsk)
		if awaitErr != nil {
			_ = started.Stop()

			return awaitErr
		}

		proc = started

		return nil
	})
	if err != nil {
		return fmt.Errorf("this step asked for a daemon and did not get one: %w", err)
	}

	// Deferred before the wait, not after it: everything below here has to stop
	// what has already been started, and a stop written on the success path only
	// is a leak on every other one.
	defer func() {
		stopErr := proc.Stop()
		if stopErr == nil {
			return
		}

		// A failing body outranks a failing shutdown: `exit status 1` is what
		// the author needs, and a complaint about a signal would bury it. But
		// when the body succeeded, a daemon that would not die is the only thing
		// that went wrong - a process still running against a handle about to be
		// released - and discarding it there tells nobody.
		if out == nil {
			out = fmt.Errorf("the step finished but its daemon would not stop: %w", stopErr)
		}
	}()

	// After the wait, because the socket does not exist until the daemon has
	// bound it - which is why this is not one of the mounts set up before the
	// step (E396).
	// Where the step will actually look, which is not always where it says: the
	// image's `/var/run` is usually a symlink (E397).
	at, err := socketTargetIn(stepRoot, inStep)
	if err != nil {
		return err
	}

	unpublish, err := publish(listen, at)
	if err != nil {
		return err
	}

	defer unpublish()

	err = body()
	if err != nil {
		return err
	}

	return nil
}
