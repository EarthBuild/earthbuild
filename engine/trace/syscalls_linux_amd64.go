//go:build linux && amd64

package trace

import "golang.org/x/sys/unix"

// auditArch is what a notification's `arch` field reads on this machine.
const auditArch = unix.AUDIT_ARCH_X86_64

// traced is every syscall on this architecture that opens or interrogates a
// path.
//
// Longer than arm64's by the legacy forms, and they are not optional: `open`,
// `stat`, `lstat`, `access` and `readlink` still exist here and a static binary
// or a busybox is entitled to use them. Tracing only the `*at` forms would work
// against everything glibc compiles and lose reads from exactly the small,
// self-contained programs a build step is most likely to run.
//
// Metadata calls are here because 𝑁 - what a step looked for and did not find -
// is not optional under I3. A step running `[ -f /etc/foo ]` and branching on
// the answer has read the *absence*, and a source recording only opens would
// serve its result against a base where the file exists.
var traced = []uint32{
	unix.SYS_OPEN,
	unix.SYS_OPENAT,
	unix.SYS_OPENAT2,
	unix.SYS_STAT,
	unix.SYS_LSTAT,
	unix.SYS_NEWFSTATAT,
	unix.SYS_STATX,
	unix.SYS_ACCESS,
	unix.SYS_FACCESSAT,
	unix.SYS_FACCESSAT2,
	unix.SYS_READLINK,
	unix.SYS_READLINKAT,

	// Executing a program *reads* it, and `execve` is not an `open`.
	//
	// Without these, a step that runs a binary from its base records the libraries
	// the loader opens and **not the binary itself** - so its observation is
	// satisfied by any base carrying the same libc, including one where the program
	// at that path is something else entirely. That is the reuse I3 forbids, and it
	// is the reason a corpus step running a freshly built binary observed nothing at
	// all (E219, E220).
	unix.SYS_EXECVE,
	unix.SYS_EXECVEAT,
}

// openers are the traced syscalls that open a path rather than interrogate one.
//
// A narrower question than `traced`, and the tracer will need it: an open says a
// step read the file's *contents*, while a stat says only that it asked about
// the entry. The green paper keeps those apart - 𝑅 is what was read - and
// recording a stat as a read would key a step on bytes it never looked at.
var openers = []uint32{
	unix.SYS_OPEN,
	unix.SYS_OPENAT,
	unix.SYS_OPENAT2,
}

var pathArgs = map[int32]int{
	// The older forms take the path first.
	unix.SYS_OPEN:     0,
	unix.SYS_STAT:     0,
	unix.SYS_LSTAT:    0,
	unix.SYS_ACCESS:   0,
	unix.SYS_READLINK: 0,

	// The *at forms take dirfd first, so the path is the second argument.
	unix.SYS_OPENAT:     1,
	unix.SYS_OPENAT2:    1,
	unix.SYS_NEWFSTATAT: 1,
	unix.SYS_STATX:      1,
	unix.SYS_FACCESSAT:  1,
	unix.SYS_FACCESSAT2: 1,
	unix.SYS_READLINKAT: 1,

	// execve takes its path first, like the older forms; execveat is an *at
	// form and takes a descriptor before it.
	unix.SYS_EXECVE:   0,
	unix.SYS_EXECVEAT: 1,
}

// openAt2NR is the one opener whose flags are not a plain argument: openat2
// takes a pointer to a `struct open_how`, so the word at that index is an
// address rather than a set of flags.
const openAt2NR = unix.SYS_OPENAT2
