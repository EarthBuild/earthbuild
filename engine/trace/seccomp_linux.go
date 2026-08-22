//go:build linux

package trace

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// The three calls seccomp user notification needs and `golang.org/x/sys/unix`
// does not wrap.
//
// Everything else in this package is ordinary Go. These are here, together, so
// that the whole of the unsafety is one screen: three pointers, each taken in
// the same expression as the call that uses it, which is the pattern
// unsafe.Pointer's own rules set out for passing a pointer to a syscall.
//
// The package carries the constants for all three and wrappers for none.
// `prctl(PR_SET_SECCOMP)` installs a filter but cannot return a listener, so
// `seccomp(2)` is the only route to one; the other two are ioctls whose argument
// is a structure, which Go cannot pass any other way.

// install puts the filter on the calling thread and returns the listener.
//
// **On the thread, not the process.** No `SECCOMP_FILTER_FLAG_TSYNC`, so the
// filter applies to this thread and anything it goes on to spawn - which is what
// a step is. A caller wanting it to cover itself must lock the thread first, and
// a caller wanting it to cover a child installs it between fork and exec.
//
// `PR_SET_NO_NEW_PRIVS` first, because the kernel requires either that or
// `CAP_SYS_ADMIN` before it will take a filter, and this engine runs without
// capabilities by design (E98). It is also the honest setting: it says this
// thread cannot gain privileges through an exec, which is true of a build step.
func install(arch uint32, traced []uint32) (int, error) {
	err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)
	if err != nil {
		return -1, fmt.Errorf(
			"set no-new-privs, which the kernel wants before a seccomp filter"+
				" from a process without CAP_SYS_ADMIN: %w", err)
	}

	f, err := filter(arch, traced)
	if err != nil {
		return -1, err
	}

	prog := unix.SockFprog{Len: uint16(len(f)), Filter: &f[0]}

	// SAFETY: `&prog` is taken in the argument list of the call that consumes
	// it, which is the documented form for passing a pointer to a syscall - the
	// compiler keeps the value alive for the duration of the call and does not
	// move it. The kernel copies the program while the syscall runs and retains
	// no pointer, so nothing here has to outlive the call.
	//
	// `prog.Filter` aliases `f`, and a `uintptr` is not a reference the garbage
	// collector can follow: the KeepAlive below is what stops `f` being
	// collected while the kernel is reading through that alias. Without it, a
	// collection between the conversion and the syscall would hand the kernel
	// freed memory, which is the one way this can be got wrong and produces no
	// error when it is.
	fd, _, errno := unix.Syscall(unix.SYS_SECCOMP,
		uintptr(unix.SECCOMP_SET_MODE_FILTER),
		uintptr(unix.SECCOMP_FILTER_FLAG_NEW_LISTENER),
		uintptr(unsafe.Pointer(&prog)))

	runtime.KeepAlive(f)

	if errno != 0 {
		return -1, fmt.Errorf(
			"install the seccomp filter with a listener: %w"+
				"\n  the kernel needs CONFIG_SECCOMP_FILTER and,"+
				" for the listener, 5.0 or newer", errno)
	}

	return int(fd), nil
}

// receive blocks until a trapped syscall arrives.
//
// The structure is zeroed before every attempt, including a retry: the kernel
// checks that the buffer it is handed is clear and answers `EINVAL` for one
// carrying the last notification. That makes an `EINTR` retry that reuses the
// buffer fail for a reason with nothing to do with the interruption.
func receive(fd int) (seccompNotif, error) {
	return receiveWith(func(n *seccompNotif) unix.Errno { return receiveInto(fd, n) })
}

// receiveWith is receive's policy, over any source of notifications.
//
// Separate for the reason receiveInto is: which errno ends this loop is the
// whole of the decision, and a decision that can only be exercised by getting
// the kernel to lose a race is a decision nobody checks.
func receiveWith(next func(*seccompNotif) unix.Errno) (seccompNotif, error) {
	for {
		// A fresh one each time round, which is the point - see receiveInto.
		var n seccompNotif

		errno := next(&n)

		switch {
		case errno == 0:
			return n, nil

		case errno == unix.EINTR:
			// A signal, not a failure. Round again with a clear buffer.
			continue

		case errno == unix.ENOENT:
			// **The notification evaporated, and that is ordinary.** The target
			// thread was killed as it was being generated, or its blocked
			// syscall was interrupted by a signal handler - seccomp_unotify(2)
			// says so. There is nothing to answer: that syscall is not going to
			// run, and the thread that would have made it is gone or has
			// restarted it. The next notification is the work.
			//
			// Read as fatal, it ended the loop and left the filter without a
			// servicer, which stops the step's next intercepted syscall in the
			// kernel for ever. The step it was caught on was `go mod download`,
			// and Go's runtime signals its own threads for preemption
			// constantly, so the window is far wider than it looks (E523).
			continue

		default:
			return seccompNotif{}, fmt.Errorf("receive a notification: %w", errno)
		}
	}
}

// receiveInto is the ioctl alone, without the clearing the caller owes it.
//
// Separate so that the kernel's requirement can be *tested* rather than
// asserted in a comment: a buffer still carrying the last notification is
// refused with EINVAL, and a `receive` that reused one would work until the
// first EINTR and then fail for a reason with nothing to do with the signal.
func receiveInto(fd int, n *seccompNotif) unix.Errno {
	// SAFETY: `n` is taken in the argument list of the call that uses it, which
	// is the documented form - the compiler keeps it alive across the call and
	// does not move it. The kernel writes through the pointer only while the
	// ioctl runs and retains nothing; `seccompNotif` holds no pointers, so
	// there is nothing further for the collector to follow.
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd),
		uintptr(uint(unix.SECCOMP_IOCTL_NOTIF_RECV)),
		uintptr(unsafe.Pointer(n)))

	return errno
}

// respond answers one notification, letting the syscall proceed.
//
// `SECCOMP_USER_NOTIF_FLAG_CONTINUE` is the whole of this engine's policy: run
// the call as though nothing had trapped it. This is an observer, and a tracer
// that could refuse a syscall would be a sandbox with a different set of
// questions to answer.
func respond(fd int, id uint64) error {
	for {
		r := seccompNotifResp{ID: id, Flags: unix.SECCOMP_USER_NOTIF_FLAG_CONTINUE}

		// SAFETY: as above. The kernel reads through `&r` during the ioctl and
		// retains nothing; `r` holds no pointers.
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd),
			uintptr(uint(unix.SECCOMP_IOCTL_NOTIF_SEND)),
			uintptr(unsafe.Pointer(&r)))

		switch {
		case errno == 0:
			return nil

		case errno == unix.EINTR:
			// **A signal, not a failure - and `receive` has always known that.**
			// The Go runtime preempts goroutines by sending SIGURG, so an ioctl
			// on a busy thread is interrupted routinely rather than rarely.
			//
			// Left unretried, one such signal ended the notification loop, and a
			// filter with nothing answering it leaves the *next* intercepted
			// syscall stopped in the kernel for ever: the step never exits, the
			// guest waits on it, and the host waits on the guest. That is the
			// stall that survived five investigations (E520).
			continue

		case errno == unix.ENOENT:
			// The target died while this engine was deciding. Nothing to answer
			// and nothing wrong: a step that exits mid-syscall is ordinary, and
			// treating it as an error would fail builds for finishing.
			return nil

		default:
			return fmt.Errorf("answer notification %d: %w", id, errno)
		}
	}
}

// stillRunning reports whether the process that made a call is still that
// process.
//
// The race this closes is real and quiet. A notification carries a pid, the path
// argument is an address in that process, and reading it means opening
// `/proc/<pid>/mem` - by which time the process may have exited and the pid been
// reused. The engine would then read some unrelated program's memory and record
// whatever was there as a path the step opened.
//
// Checked **after** the read, not before. Before proves the target was alive a
// moment ago, which is the check on the wrong side of the race; after proves the
// notification was still outstanding for the whole read, so the pid cannot have
// been recycled in the middle of it.
func stillRunning(fd int, id uint64) bool {
	// SAFETY: as above - `&id` is a pointer to a local integer, taken in the
	// argument list, read by the kernel for the duration of the ioctl only.
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd),
		uintptr(uint(unix.SECCOMP_IOCTL_NOTIF_ID_VALID)),
		uintptr(unsafe.Pointer(&id)))

	return errno == 0
}

// gettid is the calling thread's identifier.
//
// A seccomp filter is a property of a thread, so this is how anything about one
// gets checked. `golang.org/x/sys/unix` wraps it, which is the one call in this
// area it does.
func gettid() int {
	return unix.Gettid()
}

// unsafePointerTo is the address of a value, for the ioctls above.
//
// Exists so a test can make the same call without adding a fourth use of
// `unsafe` of its own - the rule is that every use is deliberate, and one place
// that already has the justification is better than two that each need it.
func unsafePointerTo(v *uint64) unsafe.Pointer {
	// SAFETY: the caller passes a pointer to a live local and uses the result
	// only in the argument list of the syscall that follows, which is the same
	// pattern as the three calls above.
	return unsafe.Pointer(v)
}
