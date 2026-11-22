//go:build linux

// Package trace observes what a step looked at while it ran.
//
// S5's source for RUN. COPY is observed by watching where a copy resolves its
// destination (engine/guest/observe.go); a RUN step is opaque by comparison,
// and what it reads decides whether a later build with a different base may
// reuse its result (green paper §3.4, I3).
//
// The mechanism is seccomp user notification: a filter installed on the step
// traps the syscalls that open or interrogate a path, and this engine reads the
// path out of the stopped process and lets the syscall proceed. It is a
// *notifier*, not a sandbox - every trapped call continues - and the filter is
// narrow so that everything else runs at full speed.
//
// # Why the structures are declared here
//
// `golang.org/x/sys/unix` carries every constant this needs and none of the
// three calls: `SECCOMP_FILTER_FLAG_NEW_LISTENER`, `SECCOMP_IOCTL_NOTIF_RECV`
// and `SECCOMP_IOCTL_NOTIF_SEND` are all defined there, while the typed ioctl
// helpers cover `Winsize`, `Termios` and a dozen others - not these - and there
// is no `Seccomp` wrapper at all. `prctl(PR_SET_SECCOMP)` cannot return a
// listener descriptor, so there is no route to one that avoids `seccomp(2)`.
//
// # Why an ABI mistake here would be quiet
//
// A struct that does not match the kernel's is not a crash. It is a field read
// from the wrong offset - a pid that is really half of an instruction pointer -
// and the engine would go on to record observations about a process that does
// not exist. **The kernel states the sizes itself**: an ioctl number encodes the
// size of its argument, so `SECCOMP_IOCTL_NOTIF_RECV = 0xc0502100` says 80
// bytes and nothing else is admissible. abi_linux_test.go asserts exactly that,
// which turns the question from one somebody reviews into one the build answers.
package trace

// seccompData is the syscall a notification is about.
//
// `struct seccomp_data` from linux/seccomp.h. Fixed by the kernel ABI and not
// this engine's to arrange: 64 bytes, no padding, every field naturally aligned.
type seccompData struct {
	// NR is the syscall number, in Arch's numbering. Signed, because a filter
	// can see -1 for a syscall the kernel does not recognise.
	NR int32
	// Arch is an AUDIT_ARCH_* value. Checked before NR is believed: the same
	// number means different syscalls on x86-64 and i386, and a process can
	// issue either.
	Arch uint32
	// InstructionPointer is where the call was made from. Unused here, and part
	// of the layout whether or not it is read.
	InstructionPointer uint64
	// Args are the syscall's six arguments, as the target passed them. A pointer
	// argument is an address in *its* address space, not this one.
	Args [6]uint64
}

// seccompNotif is one trapped syscall, waiting for an answer.
//
// `struct seccomp_notif`, 80 bytes - the size `SECCOMP_IOCTL_NOTIF_RECV`
// encodes.
type seccompNotif struct {
	// ID identifies this notification. It is also the cookie that says the
	// target is still the process that made the call: a pid can be recycled
	// between a notification arriving and its memory being read, so ID is
	// revalidated with SECCOMP_IOCTL_NOTIF_ID_VALID *after* the read and the
	// result discarded if it fails. Checking before the read would be the
	// check on the wrong side of the race.
	ID uint64
	// Pid is the process that made the call, in this engine's pid namespace.
	Pid uint32
	// Flags is unused by the kernel today and part of the layout regardless.
	Flags uint32
	// Data is the call itself.
	Data seccompData
}

// seccompNotifResp is the answer to one notification.
//
// `struct seccomp_notif_resp`, 24 bytes - what `SECCOMP_IOCTL_NOTIF_SEND`
// encodes.
type seccompNotifResp struct {
	// ID is the notification being answered, and must be the one received.
	ID uint64
	// Val is the value the syscall returns when this engine answers for it.
	// Unused while every trapped call is allowed to proceed.
	Val int64
	// Error is the errno to fail with, negative, or zero. Unused for the same
	// reason.
	Error int32
	// Flags carries SECCOMP_USER_NOTIF_FLAG_CONTINUE, which is the whole point:
	// it tells the kernel to run the syscall as though nothing had trapped it.
	// This engine observes; it does not decide what a step is allowed to do.
	Flags uint32
}

// notifSizes is what the kernel says each structure must be.
//
// Read out of the ioctl numbers rather than written down: the size field of an
// ioctl request is bits 16..29, so the kernel's own constant carries the answer
// and a table here would be a second opinion about it.
func notifSizes(recv, send uint32) (notif, resp int) {
	const sizeShift, sizeMask = 16, 0x3fff

	return int(recv >> sizeShift & sizeMask), int(send >> sizeShift & sizeMask)
}
