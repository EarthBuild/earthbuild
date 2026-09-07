package guest

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"time"
)

// watchStepListeners reports what is listening inside the step's namespace while
// the step runs, and stops when it returns.
//
// **On a timer, because the interesting moment is not the start.** The first
// version fired once before the body and caught an empty table - which said
// nothing, since `WITH DOCKER --compose` brings the service up as part of the
// step, after that point. The step that hangs waits for a port; these fire while
// it is waiting.
//
// Temporary, for E967. Remove with the diagnosis.
func watchStepListeners(netAt string) func() {
	if netAt == "" {
		return func() {}
	}

	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		for _, after := range []time.Duration{30 * time.Second, 90 * time.Second} {
			select {
			case <-done:
				return
			case <-time.After(after):
				sayStepListeners(netAt)
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
	}
}

// sayStepListeners prints what is listening inside the step's network namespace.
//
// **Temporary, for E967.** `/proc/net/tcp` is per network namespace, so this
// needs no tool in the step and no tool on the guest beyond `ip` - which
// openStepNet has already established is present, since it made the namespace.
//
// The question it answers: with the daemon and the step provably in one
// namespace (sayNetNS), and the container reporting healthy, is the published
// port listening at all? A local address of `:1538` is 5432. Nothing there means
// the daemon never published; something there means the step cannot reach what
// it published. Remove with the diagnosis.
func sayStepListeners(netAt string) {
	if netAt == "" {
		return
	}

	// A short deadline of its own: a diagnostic that hangs would replace the
	// fault being diagnosed with itself.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The namespace name is the guest's own - openStepNet made it - so nothing
	// here comes from an Earthfile.
	cmd := osexec.CommandContext(ctx, "ip", "netns", "exec", //nolint:gosec // the name is the guest's own, see above
		filepath.Base(netAt), "cat", "/proc/net/tcp")

	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "earthbuild netns[guest]: listeners in %s: %v\n", netAt, err)

		return
	}

	fmt.Fprintf(os.Stderr, "earthbuild netns[guest]: /proc/net/tcp in %s:\n%s\n",
		netAt, out)
}
