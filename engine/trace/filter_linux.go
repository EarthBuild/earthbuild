//go:build linux

package trace

import (
	"fmt"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// Offsets into seccomp_data, which the filter sees as a flat buffer.
const (
	offsetNR   = 0
	offsetArch = 4
)

// program is the filter this engine installs, as instructions rather than bytes.
//
// The shape is the whole of it:
//
//	ld  [arch]
//	jne auditArch -> notify        ; a syscall this filter cannot read
//	ld  [nr]
//	jeq nr₀ -> notify
//	…
//	jeq nrₙ -> notify
//	ret ALLOW
//	ret USER_NOTIF
//
// # Why a foreign architecture notifies rather than passing
//
// A process can issue syscalls in another architecture's numbering - i386 on
// x86-64 is the ordinary case - and `nr` then means something else entirely.
// The usual advice for a *security* filter is to kill such a process, and that
// is wrong here twice over: this is an observer, and killing a step for using a
// 32-bit binary would break builds for no safety gained.
//
// Passing it silently is the other wrong answer, and the more tempting one. A
// syscall this filter cannot read is a read it cannot record, and an observation
// missing a read is precisely the false hit I3 exists to prevent (§3.4). So it
// notifies: the tracer sees a call it cannot interpret, declares the observation
// incomplete, and lets it through. The step still runs; it costs an L2 miss
// instead of a wrong answer.
//
// The price is a round trip per syscall for a foreign-architecture process,
// which is slow and bounded to steps that run one. Correct and slow beats fast
// and quietly wrong - and the alternative is not "fast", it is "unable to say
// what it did not see".
// jumpPreamble and maxJump are the program's shape and its encoding's limit.
//
// `preamble` inside `program` is the same three instructions; named here too
// because `filter` has to know the distance before `program` is allowed to
// compute it. maxJump is what a `uint8` skip field can express.
const (
	jumpPreamble = 3
	maxJump      = 255
)

func program(arch uint32, traced []uint32) []bpf.Instruction {
	// Indices, so the jumps are derived rather than counted by hand. Layout is
	// [0] load arch, [1] arch test, [2] load nr, [3..3+n) syscall tests,
	// then allow, then notify.
	const preamble = 3

	n := len(traced)
	allowAt := preamble + n
	notifyAt := allowAt + 1

	out := make([]bpf.Instruction, 0, notifyAt+1)

	out = append(out,
		bpf.LoadAbsolute{Off: offsetArch, Size: 4},
		// Skip to notify when this is *not* the architecture we can read.
		bpf.JumpIf{
			Cond: bpf.JumpNotEqual,
			Val:  arch,
			// In range because `filter` refuses a program whose furthest jump
			// exceeds what this byte holds, before calling here (E629).
			SkipTrue:  uint8(notifyAt - 1 - 1), //nolint:gosec // bounded by filter
			SkipFalse: 0,
		},
		bpf.LoadAbsolute{Off: offsetNR, Size: 4},
	)

	for i, nr := range traced {
		at := preamble + i

		out = append(out, bpf.JumpIf{
			Cond:      bpf.JumpEqual,
			Val:       nr,
			SkipTrue:  uint8(notifyAt - at - 1), //nolint:gosec // bounded by filter, see above
			SkipFalse: 0,
		})
	}

	return append(out,
		bpf.RetConstant{Val: unix.SECCOMP_RET_ALLOW},
		bpf.RetConstant{Val: retUserNotif},
	)
}

// retUserNotif is SECCOMP_RET_USER_NOTIF.
//
// Spelled out because `golang.org/x/sys/unix` defines the other return actions
// and not this one. Taken from linux/seccomp.h, where it is the action that
// hands the call to a listener rather than deciding it.
const retUserNotif = 0x7fc00000

// filter assembles the program into what seccomp(2) takes.
//
// `bpf.RawInstruction` and `unix.SockFilter` are the same four fields in the
// same order and are copied one by one. They could be reinterpreted instead -
// the layouts match - and that would be an `unsafe` for a loop over a dozen
// instructions run once per step.
func filter(arch uint32, traced []uint32) ([]unix.SockFilter, error) {
	// **Checked before the jumps are computed, and against the encoding rather
	// than the kernel.** `program` writes its skip distances into `uint8`
	// fields, and the bound below is 4096 because that is what the kernel takes
	// - so a program between 256 and 4096 instructions passed every check and
	// had its jumps silently truncated (gosec G115).
	//
	// A seccomp filter that jumps to the wrong instruction does not fail: it
	// traps the wrong syscalls, and the tracer then reports a set of reads that
	// is not the set the step made. An observation missing a read is the false
	// hit I3 exists to prevent (§3.4), so this is a refusal rather than a
	// truncation, and it happens first.
	//
	// The furthest jump is from the architecture test to the notify verdict,
	// which is `preamble + len(traced) + 1` instructions away.
	if skip := jumpPreamble + len(traced) + 1; skip > maxJump {
		return nil, fmt.Errorf(
			"tracing %d syscalls needs a jump of %d instructions and the filter"+
				" encodes jumps in one byte (max %d)"+
				"\n  the filter would assemble with wrapped jumps and trap the"+
				" wrong calls, which is worse than refusing to build it",
			len(traced), skip, maxJump)
	}

	raw, err := bpf.Assemble(program(arch, traced))
	if err != nil {
		return nil, fmt.Errorf("assemble the seccomp filter: %w", err)
	}

	// A classic BPF program is addressed by 16-bit offsets, so this is a real
	// bound rather than a formality - though a filter this size is nowhere near
	// it, and would have to grow by three orders of magnitude to be.
	if len(raw) > 4096 {
		return nil, fmt.Errorf(
			"the seccomp filter is %d instructions, and the kernel takes 4096"+
				"\n  %d syscalls are traced; each one costs an instruction",
			len(raw), len(traced))
	}

	out := make([]unix.SockFilter, len(raw))
	for i, r := range raw {
		out[i] = unix.SockFilter{Code: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}

	return out, nil
}
