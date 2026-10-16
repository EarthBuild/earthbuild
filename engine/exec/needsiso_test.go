package exec_test

import (
	"sync"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/guest"
	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// needsIsolation skips when this machine cannot confine a step.
//
// The same question the guest's tests ask, asked through the same function
// rather than a second copy of the probe: running the suite on Linux for the
// first time produced fourteen failures across two packages, all of them
// `operation not permitted` from uid 1000, and a rule implemented twice is the
// shape this branch has spent a fortnight removing.
// The probe itself is `guest.CanIsolate`, and the namespace is `nstest.In`, so
// what is duplicated here is a call and not a rule - Go's test-package boundary
// forbids reaching the guest package's own test helper, and a second copy of
// the *probe* is what this comment used to be about.
func needsIsolation(t *testing.T) bool {
	t.Helper()

	if !nstest.In(t) {
		return false
	}

	isoOnce.Do(func() { errIso = guest.CanIsolate() })

	if errIso != nil {
		t.Skipf("this machine cannot isolate a step: %v", errIso)
	}

	return true
}

var (
	isoOnce sync.Once
	errIso  error
)
