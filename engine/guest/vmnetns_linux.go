//go:build linux

package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// makeNetns creates a network namespace that outlives the call, at path.
//
// **What `ip netns add` does, without `ip`.** A namespace exists only while
// something holds it: a process in it, or a file bound to it. Unsharing alone
// would give a namespace that vanished when this thread returned to its own,
// so the bind mount is what makes it a thing a step can be started into later.
//
// The thread is locked for the whole of it because `unshare` moves *the calling
// thread*, not the process. Without the lock the Go runtime could reschedule
// this goroutine onto another thread mid-way, leaving one thread in a stray
// namespace and the mount taken from the wrong one - a bug that would show up
// as a step whose network is occasionally somebody else's.
func makeNetns(path string) (err error) {
	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		return fmt.Errorf("make room for %s: %w", path, err)
	}

	// The file the namespace is bound onto has to exist first: a bind mount
	// needs a target, and an empty file is what iproute2 uses too.
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return fmt.Errorf("claim %s for a network namespace: %w", path, err)
	}

	_ = f.Close()

	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Where this thread is now, so it can be put back. Read before unsharing,
	// for the obvious reason.
	here, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		return fmt.Errorf("find this thread's network namespace: %w", err)
	}

	defer func() { _ = here.Close() }()

	err = unix.Unshare(unix.CLONE_NEWNET)
	if err != nil {
		return fmt.Errorf("make a network namespace: %w"+
			"\n  this needs CAP_SYS_ADMIN in the user namespace owning it", err)
	}

	// Back to where the thread was, whatever happens next: a thread left in a
	// step's namespace would answer some later step's syscalls there.
	defer func() {
		back := unix.Setns(int(here.Fd()), unix.CLONE_NEWNET)
		if back != nil && err == nil {
			err = fmt.Errorf("return this thread to its own network namespace: %w", back)
		}
	}()

	err = unix.Mount("/proc/thread-self/ns/net", path, "none", unix.MS_BIND, "")
	if err != nil {
		return fmt.Errorf("keep the network namespace at %s: %w", path, err)
	}

	return nil
}

// removeNetns releases a namespace made by makeNetns.
//
// Unmount then remove: the file is only a handle, and removing it while the
// mount stands leaves the namespace alive with nothing naming it.
func removeNetns(path string) error {
	err := unix.Unmount(path, unix.MNT_DETACH)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release the network namespace at %s: %w", path, err)
	}

	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}

	return nil
}
