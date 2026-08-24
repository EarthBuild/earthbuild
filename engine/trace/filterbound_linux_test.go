//go:build linux

package trace

import (
	"strings"
	"testing"
)

// A filter too big for its own jumps is refused, not assembled.
//
// **The bound that existed was the kernel's, not the encoding's.** `filter`
// refuses a program over 4096 instructions because that is what the kernel
// takes, and the jump offsets in `program` are `uint8`: a program between 256
// and 4096 instructions passes the check and has its jumps silently wrapped by
// the conversion (gosec G115).
//
// A seccomp filter that jumps to the wrong instruction does not fail - it traps
// the wrong syscalls. The tracer then observes a set of reads that is not the
// set the step made, and an observation missing a read is exactly the false hit
// I3 exists to prevent (§3.4). So this has to be a refusal, and it has to happen
// before the jumps are computed.
//
// Nineteen instructions today, from fourteen traced syscalls, so the bound is
// nowhere near - which is why nothing has noticed that it was the wrong bound.
func TestAFilterTooBigForItsJumpsIsRefused(t *testing.T) {
	t.Parallel()

	// One instruction per traced syscall, plus the preamble and the two
	// verdicts: enough of them and the skip distance leaves a byte.
	traced := make([]uint32, 300)
	for i := range traced {
		traced[i] = uint32(i)
	}

	_, err := filter(auditArch, traced)
	if err == nil {
		t.Fatal("a filter whose jumps cannot reach its verdicts was assembled")
	}

	for _, want := range []string{"300", "jump"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// And the ordinary filter is still built, or the bound is a refusal of everything.
func TestTheOrdinaryFilterIsStillAssembled(t *testing.T) {
	t.Parallel()

	out, err := filter(auditArch, traced)
	if err != nil {
		t.Fatalf("the filter this engine actually installs was refused: %v", err)
	}

	if len(out) == 0 {
		t.Error("the filter assembled to nothing")
	}
}
