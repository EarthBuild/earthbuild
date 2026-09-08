package exec

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// sandboxPrefix names the temporary directories a microVM sandbox makes.
	// Short, because a unix socket path is 108 bytes and the vsock hangs off
	// this - see Firecracker.dir.
	sandboxPrefix = "earth-fc"

	// sandboxLock is the file whose flock says a build still owns the
	// directory. Inside it, so removing the directory removes the claim.
	sandboxLock = ".lock"

	// sandboxGrace is how long a directory is left alone before it can be
	// considered abandoned.
	//
	// **There is an instant where a live sandbox has no lock file**, between
	// making the directory and taking the lock. Sweeping then would delete a
	// directory a build is in the middle of creating, which is the one mistake
	// this must not make. A minute is far longer than that gap and far shorter
	// than anything anybody would wait on.
	sandboxGrace = time.Minute
)

// holdSandbox claims a sandbox directory for as long as this process lives.
//
// `flock`, as claimStore uses it and for the same reason: the kernel drops it
// when the descriptor closes, however the process ended. A lock file holding a
// pid would survive a SIGKILL and make an abandoned sandbox look busy for ever,
// which is the failure mode of every lock file ever written.
func holdSandbox(dir string) (release func(), err error) {
	f, err := os.OpenFile(filepath.Join(dir, sandboxLock), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		_ = f.Close()

		return nil, err
	}

	return func() { _ = f.Close() }, nil
}

// sweepSandboxes removes the directories of sandboxes whose build is gone, and
// says how many it took.
//
// **The host-side twin of the guest's half-written layers.** A sandbox keeps
// its sockets, its VM configuration and a sparse export device here, and
// removes the lot on a clean stop - every ordinary path is tidy. A build killed
// with SIGKILL runs no code at all, so its directory stays, and nothing ever
// swept them: two were found holding 65G of sparse export device between them.
//
// Told apart by a lock rather than by age. Age cannot distinguish a long build
// from an abandoned one, and this has to be certain in the direction that
// matters - deleting a running build's directory takes its vsock socket out
// from under it. Age is used only as a grace period for the instant before a
// new sandbox takes its lock.
//
// Best effort throughout: a directory that will not be read or will not be
// removed is left for next time. Failing a build over tidying is the wrong
// trade, and the next build will try again.
func sweepSandboxes(tmp string) int {
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return 0
	}

	swept := 0

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), sandboxPrefix) {
			continue
		}

		dir := filepath.Join(tmp, e.Name())

		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < sandboxGrace {
			continue
		}

		// Taking the lock is the whole test: it succeeds only where nobody
		// holds it, which is only where the owning process has gone.
		release, err := holdSandbox(dir)
		if err != nil {
			continue
		}

		release()

		if os.RemoveAll(dir) == nil {
			swept++
		}
	}

	return swept
}
