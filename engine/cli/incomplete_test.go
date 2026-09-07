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

	// **What to do about it**, which the warning said everything except.
	//
	// It named the failure and what it costs and then stopped, so a reader who
	// believed it still had to find out for themselves that the fix is to run
	// as root. Twelve of thirteen Native CI jobs failed this way for weeks
	// while the message that explained them scrolled past every run (E922).
	//
	// Labelled `unprivileged:` rather than printed flat, as warnUnbounded
	// labels its `rootless:` line: the same warning covers a cgroups v1
	// machine, where the advice does not apply and an unlabelled imperative
	// would be wrong.
	t.Run("says how to fix it", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer

		warnIncomplete(&b, "mount /sys/fs/cgroup for the step: operation not permitted")

		out := b.String()

		if !strings.Contains(out, "CAP_SYS_ADMIN") {
			t.Errorf("the warning does not name the capability that is missing: %q", out)
		}

		if !strings.Contains(out, "root") {
			t.Errorf("the warning does not say how to get it: %q", out)
		}

		// `-P` is `--allow-privileged`, which permits `RUN --privileged` in an
		// Earthfile. It does not give this process a capability it has not got,
		// so sending a reader to it would cost them a run to find out.
		if strings.Contains(out, "-P") || strings.Contains(out, "allow-privileged") {
			t.Errorf("the warning sends the reader to a flag that cannot help: %q", out)
		}
	})

	// **Only where root is the answer.** This one warning carries every reason
	// a step's filesystem came up short - a cgroups v1 machine, a /dev/pts that
	// would not mount, a sandbox missing a feature - and root fixes exactly one
	// of them. Advice printed beside a failure it cannot fix is worse than
	// none: the reader spends a build on it and trusts the next line less.
	t.Run("no root advice where root cannot help", func(t *testing.T) {
		t.Parallel()

		for _, reason := range []string{
			"this machine is not on cgroups v2: stat /sys/fs/cgroup/cgroup.controllers: no such file or directory",
			"this sandbox has no /proc, /sys",
			"make room for /dev/pts: file exists",
		} {
			var b bytes.Buffer

			warnIncomplete(&b, reason)

			out := b.String()

			if !strings.Contains(out, reason) {
				t.Errorf("the warning dropped its reason %q: %q", reason, out)
			}

			if strings.Contains(out, "CAP_SYS_ADMIN") || strings.Contains(out, "as root") {
				t.Errorf("root advice printed for %q, which root does not fix: %q", reason, out)
			}
		}
	})

	t.Run("nil writer is not a crash", func(t *testing.T) {
		t.Parallel()

		warnIncomplete(nil, "something failed")
	})
}
