package fleet

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/core"
	"github.com/EarthBuild/earthbuild/engine/ir"
)

// A step the kernel killed for memory is refused, not failed.
//
// **The distinction §C.3 already draws, applied to the one case that was on the
// wrong side of it.** A non-zero exit is a *result* - "the step ran and said
// no" - and the build fails with its output rather than trying elsewhere. That
// is right for a compiler that found an error and wrong for a step the OOM
// killer took: nothing about the step said no, the machine ran out of room.
//
// A refusal is the answer the protocol already has for "this worker could not
// take this step", and the driver already places one elsewhere or runs it here
// (I11, E235). So an OOM becomes a slower build instead of a failed one, on a
// fleet without any new mechanism at all.
func TestAStepKilledForMemoryIsRefused(t *testing.T) {
	t.Parallel()

	reply := replyOf(core.Result{Exit: 137, OutOfMemory: true, Output: "Compiling foo"})

	if reply.Refused == "" {
		t.Fatal("a step the kernel killed for memory came back as a result," +
			"\n  so one worker running out of room fails the whole build")
	}

	if reply.Exit != 0 {
		t.Errorf("the refusal also carries exit %d, which a driver reads as a"+
			" result and fails on", reply.Exit)
	}

	if !strings.Contains(strings.ToLower(reply.Refused), "memory") {
		t.Errorf("the refusal does not say why: %q", reply.Refused)
	}
}

// A step that failed on its own merits is still a result.
//
// The half that would quietly turn error reporting off: a rule that refused
// every non-zero exit would retry a genuine compile error on every machine in
// the fleet and then fail anyway, having spent the fleet on it.
func TestAStepThatFailedOnItsOwnIsStillAResult(t *testing.T) {
	t.Parallel()

	reply := replyOf(core.Result{Exit: 1, Output: "syntax error"})

	if reply.Refused != "" {
		t.Errorf("an ordinary failure was refused (%q), so a compile error"+
			" would be retried on every machine and fail anyway", reply.Refused)
	}

	if reply.Exit != 1 {
		t.Errorf("the result lost its exit code: %d", reply.Exit)
	}
}

// And a step that succeeded is untouched.
func TestASuccessIsNotRefused(t *testing.T) {
	t.Parallel()

	if reply := replyOf(core.Result{Layer: ir.NodeID{1}}); reply.Refused != "" {
		t.Errorf("a successful step was refused: %q", reply.Refused)
	}
}
