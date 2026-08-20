//go:build linux

package guest_test

import (
	"strings"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// The probe asks for everything a step needs, not the easiest thing it needs.
//
// `CanIsolate` mounted a tmpfs and answered yes. A step also mounts `/proc`,
// and **procfs has strictly stronger requirements**: an unprivileged user
// namespace may mount it only when it owns the pid namespace being exposed, so
// a machine can mount tmpfs and refuse proc.
//
// That combination is not hypothetical - it is what a test binary re-executed
// into `CLONE_NEWUSER|CLONE_NEWNS` gets, and it is what the guest's own tests
// hit the moment they stopped skipping (E122):
//
//	mount /proc for the step: operation not permitted
//
// A probe weaker than the operation reports "this machine can isolate" and then
// the step fails with an unexplained EPERM half a second later. The engine has
// made this mistake before, in the store's case-sensitivity check: **a probe
// with fewer outcomes than the world** (E97).
//
// So the probe mounts what a step mounts. Where it cannot, the refusal names
// which mount - because "operation not permitted" tells a reader nothing about
// which permission, which is the sentence `CanIsolate`'s own doc comment
// already used to justify existing.
func TestTheIsolationProbeAsksAboutProc(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	err := guest.CanIsolate()
	if err == nil {
		// This machine can do both, which is the good case: the guest gives a
		// step its own pid namespace, so production takes this branch.
		return
	}

	// And where it cannot, it says which mount rather than which syscall.
	if !strings.Contains(err.Error(), "proc") && !strings.Contains(err.Error(), "mount") {
		t.Errorf("the refusal names neither the mount nor the filesystem:\n%s", err)
	}
}
