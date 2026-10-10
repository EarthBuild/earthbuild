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
	// **Extended attributes, because this engine hashes them into a layer.**
	// `layer/meta_unix.go` reads a path's xattrs when it captures a tree, so
	// two bases differing only in an xattr are two different layers - and a
	// step branching on one was reading base content nothing recorded. A
	// metadata call like the ones above it, and here for 𝑁's reason: what a
	// step looked for and did not find is not optional under I3.
	unix.SYS_GETXATTR,
	unix.SYS_LGETXATTR,
	// `listxattr` and `llistxattr` beside them, because they are what a reader
	// reaches for *first*. GNU coreutils' `copy_attr` enumerates the names and
	// only fetches the ones it finds, so a tree with no extended attributes is
	// a `llistxattr` returning zero and not a single `lgetxattr` - and tracing
	// the fetch alone cannot tell "nothing was read" from "read through a call
	// this engine does not trap". The set of names is base state a step can
	// branch on, which is the whole test for belonging here.
	unix.SYS_LISTXATTR,
	unix.SYS_LLISTXATTR,
	// `statfs` names a path and answers from the filesystem under it. A step
	// that branches on the answer - configure scripts do - has read something
	// about its base.
	unix.SYS_STATFS,
	// **io_uring, which is not a path-reading syscall and is how a step avoids
	// them.** A ring submits opens and reads through shared memory, so a step
	// using one reads its base without issuing a single call above. The filter
	// allows what it does not name, so those reads were not merely unrecorded -
	// they were unrecorded *silently*, and an observation that has lost part of
	// what a step read is one Κ₂ must not be derived from (I3).
	//
	// Trapped at `setup` rather than at `enter`: a ring is created once and
	// entered thousands of times, so this costs one notification per ring and
	// not one per operation. Deliberately absent from `pathArgs` - the tracer
	// then reports `a trapped syscall this engine reads no path from`, marks
	// the observation incomplete, and the step is denied an L2 hit rather than
	// given a wrong one. Slower where a build uses a ring, never wrong.
	unix.SYS_IO_URING_SETUP,
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
	// Path first, like the older forms beside them: no directory descriptor.
	unix.SYS_GETXATTR:   0,
	unix.SYS_LGETXATTR:  0,
	unix.SYS_LISTXATTR:  0,
	unix.SYS_LLISTXATTR: 0,
	unix.SYS_STATFS:     0,
}

// openAt2NR is the one opener whose flags are not a plain argument: openat2
// takes a pointer to a `struct open_how`, so the word at that index is an
// address rather than a set of flags.
const openAt2NR = unix.SYS_OPENAT2

// callNames writes this architecture's traced syscalls the way a manual page
// does. See callName.
var callNames = map[uint32]string{
	unix.SYS_OPEN:           "open",
	unix.SYS_OPENAT:         "openat",
	unix.SYS_OPENAT2:        "openat2",
	unix.SYS_STAT:           "stat",
	unix.SYS_LSTAT:          "lstat",
	unix.SYS_NEWFSTATAT:     "newfstatat",
	unix.SYS_STATX:          "statx",
	unix.SYS_ACCESS:         "access",
	unix.SYS_FACCESSAT:      "faccessat",
	unix.SYS_FACCESSAT2:     "faccessat2",
	unix.SYS_READLINK:       "readlink",
	unix.SYS_READLINKAT:     "readlinkat",
	unix.SYS_EXECVE:         "execve",
	unix.SYS_EXECVEAT:       "execveat",
	unix.SYS_GETXATTR:       "getxattr",
	unix.SYS_LGETXATTR:      "lgetxattr",
	unix.SYS_LISTXATTR:      "listxattr",
	unix.SYS_LLISTXATTR:     "llistxattr",
	unix.SYS_STATFS:         "statfs",
	unix.SYS_IO_URING_SETUP: "io_uring_setup",
}
