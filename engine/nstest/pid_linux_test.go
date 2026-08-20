//go:build linux

package nstest_test

import (
	"os"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// The child really is in the namespaces it asked for.
//
// A harness that believes it entered a namespace and did not produces confident
// wrong answers - worse than no harness. `unshare` needs `-f` for CLONE_NEWPID
// because its own process stays in the old pid namespace and only the fork
// enters the new one; `clone(2)` puts the child in directly, and this asserts
// that rather than trusting the difference.
//
// pid 1 is the observable: the first process in a pid namespace is always pid 1.
func TestTheChildIsInANewPidNamespace(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	if pid := os.Getpid(); pid != 1 {
		t.Errorf("the child is pid %d, so it is not in a new pid namespace"+
			"\n  mounting /proc inside a user namespace needs one, and without it"+
			"\n  a test that used to skip fails with `operation not permitted`", pid)
	}
}
