package cli

import (
	"bytes"
	"strings"
	"testing"
)

// A build whose steps ran with an incomplete filesystem says so, once.
//
// `mountSys` and `mountCgroup2` are allowed to fail - a step that cannot have
// /sys is still a correct step, which is why they degrade rather than refuse.
// What was missing is the other half of I11: neither the guest nor a CI log
// could say whether either mount had succeeded, so establishing that they do
// meant building the engine and probing from inside a step (E834a).
//
// Once per build, not per step, on the rule warnUnbounded already follows.
func TestAnIncompleteStepFilesystemSaysSo(t *testing.T) {
	t.Parallel()

	t.Run("silent when every mount was made", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer

		warnIncomplete(&b, "")

		if b.Len() != 0 {
			t.Errorf("a build whose mounts all succeeded printed a warning: %q", b.String())
		}
	})

	t.Run("names what is missing and what reads it", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer

		warnIncomplete(&b, "mount /sys/fs/cgroup for the step: operation not permitted")

		out := b.String()

		// The guest's own reason, because "a mount failed" leaves a reader to
		// guess between a v1 machine, a rootless build and a denied capability.
		if !strings.Contains(out, "operation not permitted") {
			t.Errorf("the warning does not carry the guest's reason: %q", out)
		}

		// What it costs, in the terms someone hits it in: a nested runtime is
		// the thing that reads this and the thing that fails without it.
		if !strings.Contains(out, "nested") {
			t.Errorf("the warning does not say what stops working: %q", out)
		}
	})

	t.Run("nil writer is not a crash", func(t *testing.T) {
		t.Parallel()

		warnIncomplete(nil, "something failed")
	})
}
