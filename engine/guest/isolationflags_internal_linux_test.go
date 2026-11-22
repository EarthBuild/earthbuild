package guest

import (
	"syscall"
	"testing"
)

// A step is in a cgroup namespace of its own.
//
// Without one it reads the machine's cgroup path out of /proc/self/cgroup -
// `0::/earthbuild.main` rather than `0::/` - and can walk the whole hierarchy
// under /sys/fs/cgroup once that is mounted. That is ambient state a step can
// observe and no key describes (I3), and it is also what stops a nested runtime
// making cgroups of its own: it would be creating them in the machine's tree
// rather than in its own (E754).
func TestAStepIsInItsOwnCgroupNamespace(t *testing.T) {
	t.Parallel()

	flags := isolationFlags(false)

	for _, want := range []struct {
		flag uintptr
		name string
	}{
		{syscall.CLONE_NEWNS, "CLONE_NEWNS"},
		{syscall.CLONE_NEWPID, "CLONE_NEWPID"},
		{syscall.CLONE_NEWUTS, "CLONE_NEWUTS"},
		{syscall.CLONE_NEWIPC, "CLONE_NEWIPC"},
		{unixCLONENEWCGROUP, "CLONE_NEWCGROUP"},
	} {
		if flags&want.flag == 0 {
			t.Errorf("a step is not confined by %s", want.name)
		}
	}

	// The network is opt-in: cutting it would break every build that fetches a
	// dependency, which is most of them.
	if flags&syscall.CLONE_NEWNET != 0 {
		t.Error("the network was cut without being asked to be")
	}

	if isolationFlags(true)&syscall.CLONE_NEWNET == 0 {
		t.Error("the network was asked to be cut and was not")
	}
}
