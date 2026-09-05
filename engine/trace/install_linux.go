//go:build linux

package trace

import (
	"errors"
	"os"
	"runtime"
)

// InstallOnSelf puts the filter on the calling thread and returns the listener.
//
// For the helper that a step is exec'd from. The sequence a caller owes this
// function is exact, and each part of it is load-bearing:
//
//  1. `runtime.LockOSThread`, **without unlocking**. A seccomp filter cannot be
//     removed, so a thread that has one must be destroyed rather than returned
//     to the scheduler - `defer runtime.UnlockOSThread()` hands a permanently
//     filtered thread back to the runtime and the next goroutine to land on it
//     inherits a filter whose listener nobody holds (E206). Exiting locked is
//     what terminates the thread.
//  2. Send the listener to whoever will answer it, over `SCM_RIGHTS`. It has to
//     leave this process, because this process is about to stop existing.
//  3. `execve` the step. **The filter survives it** - that is the point of
//     `PR_SET_NO_NEW_PRIVS`, and it is why the step ends up traced without the
//     step or the engine's exec path knowing anything about seccomp.
//
// Between 2 and 3 the thread is filtered and nothing is answering yet, so a
// syscall made in between blocks until the reader starts. Keep it to the send
// and the exec.
//
// Returned as an `*os.File` rather than a descriptor so that it closes when it
// is dropped, and so the fd-passing takes what it already takes.
//
// For the *other* arrangement - installing here and forking the step from this
// same thread, with nothing exec'd in between - use StartOnSelf, which returns a
// tracer that knows to disregard this thread's own syscalls.
func InstallOnSelf() (*os.File, error) {
	if !threadIsLocked() {
		return nil, errors.New(
			"the calling goroutine has not locked its thread" +
				"\n  a seccomp filter applies to one thread and cannot be" +
				" removed, so the goroutine must own its thread and must exit" +
				" without unlocking it" +
				"\n  call runtime.LockOSThread first, and do not defer" +
				" runtime.UnlockOSThread")
	}

	fd, err := install(auditArch, traced)
	if err != nil {
		return nil, err
	}

	return os.NewFile(uintptr(fd), "seccomp-listener"), nil
}

// threadIsLocked reports whether the caller has locked its OS thread.
//
// There is no direct way to ask, so this is the indirect one that works: a
// locked goroutine always runs on the same thread, so a thread identifier taken
// either side of a scheduling point is equal for a locked goroutine and only
// coincidentally equal for an unlocked one.
//
// A guess, and it is on the safe side of the thing it guards - a false "locked"
// costs the caller a leaked thread, a false "unlocked" costs a clear error - so
// the yield is what makes it worth having rather than a formality.
func threadIsLocked() bool {
	before := gettid()

	runtime.Gosched()

	return gettid() == before
}

// StartOnSelf installs the filter on the calling thread and traces from it.
//
// The arrangement the guest uses. A goroutine locks its thread, calls this, and
// starts the step from the same thread: a seccomp filter is inherited across
// fork and carried through exec, so the step is traced and the rest of the
// engine is not - with no helper binary, and so nothing of the engine's inside
// the step's own filesystem, which `SysProcAttr.Chroot` would otherwise require.
//
// The returned tracer disregards this thread's syscalls, which is why this
// exists rather than `NewTracer(InstallOnSelf())`. That thread goes on doing the
// engine's work - `exec.Cmd` alone opens /dev/null on it for a nil Stdout - and
// every bit of it would otherwise be recorded as something the step read (E211).
//
// The same rule as InstallOnSelf and for the same reason: lock the thread, never
// unlock it, and let the goroutine exit so the runtime destroys it. Filters
// accumulate, so a thread cannot be reused for a second step.
func StartOnSelf() (*Tracer, error) {
	listener, err := InstallOnSelf()
	if err != nil {
		return nil, err
	}

	// fromFile, not NewTracer: the tracer has to *own* the file, because an
	// *os.File closes its descriptor from a finaliser and a tracer holding only
	// the number would lose the listener at the next collection (E215).
	t := fromFile(listener)
	t.mine = uint32(gettid()) //nolint:gosec // a thread id is not negative

	return t, nil
}
