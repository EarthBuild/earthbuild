package guest

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/nstest"
)

// A step that asked to be cut off gets an empty network namespace.
//
// `isolate` has taken a `dropNet` since it was written, and `Server.DropNet`
// has carried one - and **nothing anywhere set either**, while
// `RUN --network=none` was refused as an engine gap. Written and unreachable,
// with a refusal standing in front of it. This is the bottom half of the wire,
// asserted where the flag turns into a clone flag.
//
// Both directions, because the default matters as much: cutting the network
// off by accident breaks every build that fetches a dependency, which is most
// of them, and it breaks them a long way from here.
//
// Inside a user namespace, where euid reads as 0 - isolate refuses outright
// otherwise, and a test that skipped on that would assert nothing on the
// machine where this runs.
func TestDropNetAddsANetworkNamespace(t *testing.T) {
	if !nstest.In(t) {
		return
	}

	for _, tc := range []struct {
		name string
		drop bool
	}{
		{"asked to be cut off", true},
		{"an ordinary step", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/true")

			err := isolate(cmd, t.TempDir(), tc.drop)
			if err != nil {
				t.Fatalf("isolate: %v", err)
			}

			got := cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWNET != 0
			if got != tc.drop {
				t.Errorf("CLONE_NEWNET applied = %v, want %v", got, tc.drop)
			}

			// The rest of the confinement is not conditional on this one: a
			// step that asked for no network must not thereby lose its mount or
			// pid namespace, which is the shape of mistake a single flags
			// expression invites.
			for _, want := range []struct {
				flag uintptr
				name string
			}{
				{syscall.CLONE_NEWNS, "CLONE_NEWNS"},
				{syscall.CLONE_NEWPID, "CLONE_NEWPID"},
				{syscall.CLONE_NEWUTS, "CLONE_NEWUTS"},
				{syscall.CLONE_NEWIPC, "CLONE_NEWIPC"},
			} {
				if cmd.SysProcAttr.Cloneflags&want.flag == 0 {
					t.Errorf("%s is missing", want.name)
				}
			}
		})
	}
}
