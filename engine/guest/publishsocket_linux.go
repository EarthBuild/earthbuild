//go:build linux

package guest

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// publishSocket makes a daemon's socket appear inside the step, at the path the
// step's client looks for.
//
// A bind, and it has to happen **after** the daemon is up: the source does not
// exist until the daemon has bound it, so this cannot be one of the mounts set
// up before the step. That ordering is the whole reason this is a separate
// mechanism rather than another entry in `bindMounts`.
//
// A bind of a socket is what an inherited daemon already travels through
// (E386), so the mechanism is proven; what is new is only when it happens.
//
// The target is created first because a bind needs one, and it is an ordinary
// empty file: the mount covers it, and if the mount ever failed the step would
// find a file that is not a socket and say so, rather than finding nothing and
// blaming the daemon.
func publishSocket(from, to string) (func(), error) {
	f, err := os.OpenFile(to, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return func() {}, fmt.Errorf("make somewhere to bind the daemon's socket: %w", err)
	}

	_ = f.Close()

	if err := unix.Mount(from, to, "", unix.MS_BIND, ""); err != nil {
		return func() {}, fmt.Errorf("bind the daemon's socket into the step: %w", err)
	}

	return func() {
		_ = unix.Unmount(to, unix.MNT_DETACH)
		_ = os.Remove(to)
	}, nil
}
