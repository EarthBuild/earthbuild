package guest

import (
	"syscall"
	"testing"
)

// Root does not get a user namespace nested inside its own.
//
// At one level of user namespace every shape works. Adding a *pid* namespace
// breaks the inner one with `fork/exec: permission denied`, because the parent
// writes `/proc/<pid>/uid_map` through a `/proc` that does not match the pid
// namespace, so the child never receives its mapping and execs as nobody (E377).
// The guest is often already in a user namespace, which makes this the common
// case rather than the exotic one.
//
// The mount namespace is asked for either way: the private `/run` is the other
// half of what the daemon needs, and root-in-a-namespace already carries the
// capability to mount one. So the assertion is not "nothing happens for root" -
// it is that exactly one of the two is skipped.
func TestRootIsNotGivenANestedUserNamespace(t *testing.T) {
	t.Parallel()

	asRoot := namespacedAs(&syscall.SysProcAttr{}, 0, 0)

	if asRoot.Cloneflags&syscall.CLONE_NEWUSER != 0 {
		t.Error("root was given a user namespace inside its own: the daemon's" +
			" children exec as nobody, and the error names the exec rather" +
			" than the namespace")
	}

	if len(asRoot.UidMappings) != 0 || len(asRoot.GidMappings) != 0 {
		t.Errorf("root was given id mappings (%d uid, %d gid) for a namespace"+
			" it is not in", len(asRoot.UidMappings), len(asRoot.GidMappings))
	}

	// The mount namespace is the half that is wanted either way.
	if asRoot.Cloneflags&syscall.CLONE_NEWNS == 0 {
		t.Error("root was not given a mount namespace, so the daemon has no" +
			" private /run - which is the other half of what it needs")
	}

	// And an ordinary user still gets one, or the skip has swallowed the rule
	// rather than narrowed it.
	asUser := namespacedAs(&syscall.SysProcAttr{}, 1000, 1000)
	if asUser.Cloneflags&syscall.CLONE_NEWUSER == 0 {
		t.Error("a non-root uid was not given a user namespace, so root inside" +
			" the daemon maps to nothing outside it")
	}
}
