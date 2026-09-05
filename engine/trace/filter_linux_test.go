//go:build linux

package trace

import (
	"encoding/binary"
	"testing"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// call renders one seccomp_data as the *virtual machine* reads it.
//
// The filter sees a flat buffer, so this is the buffer - not a struct that
// happens to have the right fields. Building it any other way would be testing
// the test's idea of the layout.
//
// **Big-endian, and the kernel is not.** `golang.org/x/net/bpf` implements
// classic BPF's packet semantics, where an absolute load is network byte order;
// seccomp hands the kernel's interpreter a struct and its loads are native. The
// instructions are identical and only the buffer differs, so writing it in the
// VM's order is what makes the VM see the u32 the kernel would see - the bytes
// here are the opposite way round from a real seccomp_data *on purpose*.
//
// Getting this wrong is not loud. Little-endian bytes make the architecture
// check fail for every input, so every call notifies - and
// TestEveryTracedSyscallNotifies **passes**, because notifying is what it
// asserts. It passed for exactly that reason, and only TestAnUntracedSyscallIsAllowed
// failing said so (E205).
func call(nr int32, arch uint32) []byte {
	b := make([]byte, 64)

	binary.BigEndian.PutUint32(b[offsetNR:], uint32(nr))
	binary.BigEndian.PutUint32(b[offsetArch:], arch)

	return b
}

// run executes the filter and reports what it decided.
func run(t *testing.T, data []byte) uint32 {
	t.Helper()

	vm, err := bpf.NewVM(program(auditArch, traced))
	if err != nil {
		t.Fatalf("the filter is not a valid program: %v", err)
	}

	out, err := vm.Run(data)
	if err != nil {
		t.Fatalf("running the filter: %v", err)
	}

	return uint32(out)
}

// The filter is executed rather than inspected.
//
// A seccomp filter is a jump table whose offsets are computed, and the failure
// it invites is an offset one out - which lands on a different `ret` and is
// perfectly valid BPF. Reading the bytes back and asserting they are the bytes
// that were written proves the assembler works; it says nothing about whether
// the program decides correctly.
//
// So it is run. `golang.org/x/net/bpf` carries a virtual machine for classic
// BPF, which is what a seccomp filter is, and a seccomp_data is a flat 64-byte
// buffer - so the thing under test here is the same program the kernel would
// execute, on the same input.
func TestEveryTracedSyscallNotifies(t *testing.T) {
	t.Parallel()

	for _, nr := range traced {
		if got := run(t, call(int32(nr), auditArch)); got != retUserNotif {
			t.Errorf("syscall %d returns %#x, want USER_NOTIF %#x"+
				"\n  a read through it would go unobserved", nr, got, retUserNotif)
		}
	}
}

// Everything else runs at full speed, which is the point of a narrow filter.
//
// `write`, `close` and `mmap` are the ordinary traffic of a build step. A filter
// that notified on those would be correct and useless: every one of them would
// become a round trip through this engine.
func TestAnUntracedSyscallIsAllowed(t *testing.T) {
	t.Parallel()

	for _, nr := range []int32{unix.SYS_WRITE, unix.SYS_CLOSE, unix.SYS_MMAP} {
		if got := run(t, call(nr, auditArch)); got != unix.SECCOMP_RET_ALLOW {
			t.Errorf("syscall %d returns %#x, want ALLOW %#x"+
				"\n  ordinary traffic would trap", nr, got, unix.SECCOMP_RET_ALLOW)
		}
	}
}

// A foreign architecture notifies, so that what cannot be read can be declared.
//
// A process may issue syscalls in another architecture's numbering - i386 on
// x86-64 is the ordinary case - and `nr` then means something else entirely.
// Passing those silently is the tempting answer and the wrong one: a syscall
// this filter cannot read is a read it cannot record, and an observation missing
// a read is the false hit I3 exists to prevent.
//
// Notifying turns it into a *declared* gap. The tracer sees a call it cannot
// interpret, marks the observation incomplete, and lets it through - an L2 miss
// rather than a wrong answer.
//
// Asserted with a syscall number that **is** traced, so a filter that checked
// only `nr` and not `arch` would allow it and fail here. Using an untraced
// number would pass either way and prove nothing.
func TestAForeignArchitectureNotifies(t *testing.T) {
	t.Parallel()

	const notThisOne = unix.AUDIT_ARCH_I386

	if notThisOne == auditArch {
		t.Skip("this machine is i386, so there is no foreign arch to test with")
	}

	got := run(t, call(int32(traced[0]), notThisOne))
	if got != retUserNotif {
		t.Errorf("a syscall in another architecture's numbering returns %#x,"+
			" want USER_NOTIF %#x\n  reads through it would be missed"+
			" *and* unrecorded, which I3 forbids", got, retUserNotif)
	}
}

// The filter assembles, and to something the kernel will take.
func TestTheFilterAssembles(t *testing.T) {
	t.Parallel()

	f, err := filter(auditArch, traced)
	if err != nil {
		t.Fatal(err)
	}

	// Preamble of three, one test per syscall, two returns.
	if want := 3 + len(traced) + 2; len(f) != want {
		t.Errorf("the filter is %d instructions, want %d", len(f), want)
	}

	// A seccomp filter's length is passed to the kernel as a uint16, so this is
	// the real ceiling and not the 4096 instruction limit.
	if len(f) > 0xffff {
		t.Errorf("the filter has %d instructions and its length is a uint16",
			len(f))
	}
}

// The jump arithmetic holds for any number of traced syscalls.
//
// `traced` is chosen by build tag, so a test that only ever runs the host's list
// exercises one length: seven on arm64, twelve here. The offsets are computed
// from that length, and an error in the computation could be invisible at one
// value and wrong at the other - and the architecture that is not this one is
// compile-checked and never executed.
//
// So the builder is driven directly, with lists it would never be given. The
// empty one is not a real configuration and is the interesting case anyway: with
// no syscall tests at all, the architecture check jumps straight over the
// `ALLOW` to the `USER_NOTIF`, which is the shortest path through the table and
// the one where an off-by-one has the least room to hide.
func TestTheJumpsHoldForAnyNumberOfTracedSyscalls(t *testing.T) {
	t.Parallel()

	const fakeArch = 0xdeadbe00

	for _, n := range []int{0, 1, 2, 7, 12, 40} {
		list := make([]uint32, n)
		for i := range list {
			// Numbers no architecture uses, so a match can only come from the
			// table rather than from coinciding with a real syscall.
			list[i] = uint32(0x1000 + i)
		}

		vm, err := bpf.NewVM(program(fakeArch, list))
		if err != nil {
			t.Fatalf("%d traced: not a valid program: %v", n, err)
		}

		decide := func(nr int32, arch uint32) uint32 {
			out, err := vm.Run(call(nr, arch))
			if err != nil {
				t.Fatalf("%d traced: running: %v", n, err)
			}

			return uint32(out)
		}

		// A foreign architecture notifies whatever the table holds.
		if got := decide(0x1000, fakeArch+1); got != retUserNotif {
			t.Errorf("%d traced: a foreign arch returns %#x, want USER_NOTIF",
				n, got)
		}

		// Something absent from the table is allowed, including when the table
		// is empty and every syscall is absent from it.
		if got := decide(0x9999, fakeArch); got != unix.SECCOMP_RET_ALLOW {
			t.Errorf("%d traced: an untraced syscall returns %#x, want ALLOW",
				n, got)
		}

		// And each entry notifies - the first and last especially, since they
		// sit at the two ends of the computed offsets.
		// First, middle and last, which are the two ends of the computed
		// offsets and one in between. Bounded at both ends: with an empty table
		// this set is {0, 0, -1} and every one of them is out of range.
		for _, i := range []int{0, n / 2, n - 1} {
			if i < 0 || i >= n {
				continue
			}

			if got := decide(int32(list[i]), fakeArch); got != retUserNotif {
				t.Errorf("%d traced: entry %d returns %#x, want USER_NOTIF",
					n, i, got)
			}
		}
	}
}
