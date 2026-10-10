//go:build linux

package guest

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// StopMachine ends the process holding this sandbox open.
//
// Only where the host said so, and only when what is at PID 1 still looks like
// something the engine started: two independent conditions before an
// irreversible signal, because the cost of being wrong is not a leaked VM but a
// signalled init.
//
// SIGTERM rather than SIGKILL: the runtime around it has a container to tear
// down, and a keep-alive that will not stop on a TERM is a keep-alive this
// should not be escalating against.
//
// Best effort by design. A machine that will not stop is the state this engine
// was already in, so a failure here costs what it cost before and must not turn
// an idle timeout into an error nobody is listening for.
func StopMachine() error {
	if !OwnsMachine() {
		return nil
	}

	if os.Getpid() == 1 {
		// The guest *is* the keep-alive, so exiting is already the whole of
		// stopping the machine and signalling itself would only race that.
		return nil
	}

	err := unix.Kill(1, unix.SIGTERM)
	if err != nil {
		return fmt.Errorf("stop the machine holding this sandbox open: %w", err)
	}

	return nil
}
