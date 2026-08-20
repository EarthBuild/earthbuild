package guest

import (
	"fmt"
	"path/filepath"
)

// checkDaemon decides whether this guest will honour a daemon request.
//
// **Refusing is the whole point.** A guest that accepts the request and quietly
// does not start anything hands the step a socket with nothing behind it, and
// the author reads a message about Docker being unreachable rather than about
// this engine declining to run it (I10). The protocol version stops an *old*
// guest doing that; this stops a current one on a platform that cannot.
//
// nil is the ordinary case and passes: almost no step wants a daemon.
func checkDaemon(d *Daemon) error {
	if d == nil {
		return nil
	}

	switch {
	case d.Root == "":
		return fmt.Errorf("a daemon was asked for with no root to keep its storage in")

	case d.Socket == "":
		return fmt.Errorf("a daemon was asked for with no socket to listen on")

	case !filepath.IsAbs(d.Root):
		return fmt.Errorf("a daemon's root must be absolute inside the step, and %q is not",
			d.Root)

	case !filepath.IsAbs(d.Socket):
		return fmt.Errorf("a daemon's socket must be absolute inside the step, and %q is not",
			d.Socket)
	}

	if why := cannotRunDaemon(); why != "" {
		return fmt.Errorf("this guest cannot run a daemon inside a step: %s", why)
	}

	return nil
}
