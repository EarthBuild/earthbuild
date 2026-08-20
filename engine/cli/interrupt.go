package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// InterruptContext is a build context that an interrupt cancels, once.
//
// `cmd/earth-native` called `cli.Run` with `context.Background()`, so a build
// could be abandoned promptly - TestACancelledBuildReturnsPromptly says so - and
// nothing in a terminal could ask it to. Ctrl-C killed the process where it
// stood, which leaves the guest's mounts up and its handles unreleased;
// `unmountAll` exists because a mount left behind keeps a root busy for as long
// as the machine is up.
//
// **The handler stands aside once it has fired.** A signal handler that stays
// installed makes a build which ignores the first Ctrl-C unkillable by the
// second, and a wedged build is exactly when somebody presses it twice. So the
// first interrupt asks and the second is the operating system's business again.
//
// SIGTERM as well as SIGINT: a build in CI is stopped by a supervisor, not by a
// keyboard, and it deserves the same tidy exit.
func InterruptContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)

	stop := func() {
		signal.Stop(ch)
		cancel()
	}

	go func() {
		select {
		case <-ch:
			// Stand aside first, then cancel: between the two, a second signal
			// should already be the default action rather than something this
			// process has an opinion about.
			signal.Stop(ch)
			cancel()
		case <-ctx.Done():
			signal.Stop(ch)
		}
	}()

	return ctx, stop
}
