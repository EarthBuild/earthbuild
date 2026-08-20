package guest

import (
	"fmt"
	"os"
	"path/filepath"
)

// sockaddrLimit is the longest path a unix socket may be bound to.
//
// `sun_path` is 108 bytes on Linux; containerd refuses at 104 and is the
// strictest thing in the chain, so 104 is the number this engine keeps to
// (E375). Both limits were met the hard way, three increments apart.
const sockaddrLimit = 104

// shortSocket gives a daemon somewhere to listen that fits in a sockaddr, and a
// way to remove it.
//
// **Not under the step.** The step's root is a store, a handle and an overlay
// before anything of the daemon's is appended, which is past the limit on its
// own (E396). The socket is bound *into* the step once the daemon has created
// it - a bind's target is opened by path and never named in a `sockaddr`, so the
// length that matters is only this one.
//
// One per call, because two steps run at once: a shared path would have the
// second daemon fail to bind, or succeed and be reached by the first step's
// client.
func shortSocket() (string, func(), error) {
	dir, err := os.MkdirTemp("", "eb")
	if err != nil {
		return "", func() {}, fmt.Errorf("make somewhere for the daemon to listen: %w", err)
	}

	return filepath.Join(dir, "d.sock"), func() { _ = os.RemoveAll(dir) }, nil
}
