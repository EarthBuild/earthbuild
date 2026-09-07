package guest

import (
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/nstest"
)

var (
	isoOnce sync.Once
	errIso  error
)

// NeedsIsolation skips when this process cannot create the namespaces a
// confined step runs in.
//
// Running the engine's suite on a Linux machine for the first time produced
// eight failures, all of them one sentence:
//
//	mount /proc for the step: operation not permitted
//
// Which is uid 1000 without CAP_SYS_ADMIN, and is a fact about the machine
// rather than about the code. **A Linux developer's first `go test ./engine/...`
// reported eight defects that were not there** - the same class as every check
// this branch has fixed for failing where nothing is wrong, and the reason it
// went unnoticed is that on macOS these tests run inside a VM as root.
//
// Exported because half these tests live in the external test package and
// half in this one, and both need the same answer.
//
// Probed once, and the probe is the operation itself: no list of capabilities
// to consult and get wrong, and no assumption that root is the only way to have
// them - a machine granting CAP_SYS_ADMIN to an unprivileged user runs the
// tests, which asking `Getuid() == 0` would have refused.
// **What changed.** The probe was right and its conclusion was half of one: a
// process that cannot isolate *now* may be able to inside a user namespace, and
// E98 measured that it can - the capability is checked in the namespace the
// operation happens in. Skipping instead meant twelve tests of the guest's
// isolation machinery, which is the heart of the guest, never ran on Linux by
// the gate. They ran on macOS only because the tests there are inside a VM as
// root.
//
// Returns whether the caller should run its body: true inside the namespace,
// false in the parent, which has already reported the child's outcome.
func NeedsIsolation(t *testing.T) bool {
	t.Helper()

	if !nstest.In(t) {
		return false
	}

	isoOnce.Do(func() { errIso = CanIsolate() })

	if errIso != nil {
		t.Skipf("this machine cannot isolate a step: %v", errIso)
	}

	return true
}
