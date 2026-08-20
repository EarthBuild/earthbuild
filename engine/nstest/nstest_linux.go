//go:build linux

// Package nstest runs a test inside a user namespace.
//
// It exists because unprivileged overlayfs works only there (E98), so every
// test needing a real overlay skipped on Linux unless somebody invoked the
// binary under `unshare -Umr` - including TestOverlayConforms, the
// conformance suite for the materialiser S3 calls real on Linux.
//
// A skip that depends on how the binary was invoked is not coverage.
package nstest

import (
	"os"
	osexec "os/exec"
	"strings"
	"syscall"
	"testing"
)

// nsMarker tells a re-executed test binary it is already inside a namespace.
const nsMarker = "EARTH_TEST_IN_USERNS"

// inUserNamespace runs this test again inside a user namespace, and reports
// what happened there.
//
// **Why a test has to do this.** Unprivileged overlayfs works, and it works
// because the capability is checked in the namespace the mount happens in
// (E98) - so the guest can mount and a plain `go test` process cannot. Every
// test that needs a real overlay therefore skips on Linux unless somebody
// remembers to run the binary under `unshare -Umr`.
//
// A skip that depends on how the binary was invoked is not coverage. The join
// this guards - that what the observer digests inside a mount equals what the
// view digests inside the layer store - is the one the whole L2 tier rests on,
// and "checked when someone remembers" is how it would rot.
//
// Returns true in the child, so the caller runs the body; the parent reports
// the child's outcome and returns false.
func In(t *testing.T) bool {
	t.Helper()

	if os.Getenv(nsMarker) != "" {
		return true
	}

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot find this test binary to re-run it: %v", err)
	}

	// Exactly this test, so the child does not re-run the whole package - and
	// anchored, so a test whose name is a prefix of another does not drag it in.
	cmd := osexec.Command(self, "-test.run", "^"+t.Name()+"$", "-test.v") //nolint:gosec // this binary
	cmd.Env = append(os.Environ(), nsMarker+"=1")

	// One uid, which is all an unprivileged process may map on its own and all
	// an overlay mount needs: root *in this namespace* is what carries
	// CAP_SYS_ADMIN there. The delegated-range path (E105) is the guest's
	// business and buys nothing here.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// The same three the engine asks for (`exec.unprivilegedNamespace`).
		// CLONE_NEWPID is not optional: mounting /proc inside a user namespace
		// requires one, and without it a test that used to skip started
		// failing with `mount /proc for the step: operation not permitted` -
		// a harness in a *different* world from the thing it tests, which is
		// worse than no harness because it produces confident wrong answers.
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID,
		UidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getuid(), Size: 1},
		},
		GidMappings: []syscall.SysProcIDMap{
			{ContainerID: 0, HostID: os.Getgid(), Size: 1},
		},
		GidMappingsEnableSetgroups: false,
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		// A child that never started is a machine that cannot, not a test that
		// failed. Both arrive here as a non-zero exit, and telling them apart
		// is the difference between "this container has no user namespaces" and
		// twelve failing tests in engine/guest, none of which ran (E158).
		if unstartable(out) {
			t.Skipf("this machine will not make a user namespace, so nothing ran: %s",
				whyUnstartable(err))
		}

		if strings.Contains(string(out), "SKIP") {
			t.Skipf("inside a user namespace: %s", trimOutput(out))
		}

		t.Errorf("inside a user namespace:\n%s", out)

		return false
	}

	// A child that skipped is a skip here: the machine could not do it, and
	// reporting PASS for a body that never ran is the failure this file exists
	// to remove.
	if strings.Contains(string(out), "--- SKIP") {
		t.Skipf("inside a user namespace: %s", trimOutput(out))
	}

	return false
}

func trimOutput(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		return s[:400] + " …"
	}

	return s
}

// unstartable reports whether the child never got as far as running a test.
//
// `go test` announces itself: a binary that ran says `=== RUN`, `--- FAIL`,
// `PASS` or `FAIL`. None of that means nothing executed, so the exit status is
// about the fork and not about the engine.
//
// Deliberately a check for *evidence of running* rather than a match on the
// kernel's message. "operation not permitted" is what this machine says today;
// a different kernel, a seccomp filter or a sandbox will phrase its refusal
// differently, and a test harness that recognises one wording turns every other
// into a false failure.
func unstartable(out []byte) bool {
	for _, sign := range []string{"=== RUN", "--- FAIL", "--- PASS", "--- SKIP", "\nPASS", "\nFAIL"} {
		if strings.Contains(string(out), sign) {
			return false
		}
	}

	return true
}

// whyUnstartable is the refusal, for a skip message that names its cause.
func whyUnstartable(err error) string {
	if err == nil {
		return "no reason given"
	}

	return err.Error()
}
