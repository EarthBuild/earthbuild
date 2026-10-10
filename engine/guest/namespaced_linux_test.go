//go:build linux

package guest

import (
	"syscall"
	"testing"
)

// A process that is already root does not ask for a user namespace.
//
// The namespace exists for one reason: `dockerd` refuses to start unless it is
// root (E373). A guest that is *already* root - which it is whenever it is
// itself inside a user namespace, and E105 says it often is - has that reason
// satisfied, and asking again is asking for a nested namespace nobody needs.
//
// Nesting is not free. Bisected on a real kernel: at one level of user namespace
// every shape works, and adding a **pid** namespace breaks the inner one with
// `fork/exec: permission denied` - the parent writes `/proc/<pid>/uid_map`
// through a `/proc` that does not match the pid namespace, so the child never
// receives its mapping and execs as nobody. The same root cause as `selfExe`'s,
// one level deeper (E377).
//
// The mount namespace is still asked for either way: the private `/run` is the
// other half of what the daemon needs, and root-in-a-namespace already carries
// the capability to mount one.
func TestAProcessThatIsAlreadyRootDoesNotNestANamespace(t *testing.T) {
	t.Parallel()

	asRoot := namespacedAs(&syscall.SysProcAttr{}, 0, 0)

	if asRoot.Cloneflags&syscall.CLONE_NEWUSER != 0 {
		t.Error("root asked for a user namespace it does not need, and a nested one" +
			" is what fails inside a pid namespace")
	}

	if asRoot.Cloneflags&syscall.CLONE_NEWNS == 0 {
		t.Error("no mount namespace, so there is nowhere to put the private /run")
	}

	if asRoot.UidMappings != nil {
		t.Error("a mapping was requested without a namespace to apply it to")
	}

	asUser := namespacedAs(&syscall.SysProcAttr{}, 1000, 1000)

	if asUser.Cloneflags&syscall.CLONE_NEWUSER == 0 {
		t.Error("an ordinary user got no user namespace, so the daemon will refuse" +
			" to start for want of being root")
	}

	if len(asUser.UidMappings) != 1 || asUser.UidMappings[0].HostID != 1000 {
		t.Errorf("the mapping does not name the invoking user: %+v", asUser.UidMappings)
	}

	if asUser.GidMappingsEnableSetgroups {
		t.Error("setgroups was left enabled, and the kernel refuses a gid map then")
	}
}
