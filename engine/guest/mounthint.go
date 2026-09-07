package guest

import (
	"errors"
	"syscall"
)

// sysAdminHint explains the one failure that has a specific cause, and says
// nothing about the rest.
//
// EPERM is almost always the nesting case: an inner build - `earth` running
// inside a WITH DOCKER step - is root in its container and still cannot make a
// mount namespace or mount anything in one, because a container without
// `CAP_SYS_ADMIN` may not. `operation not permitted` alone sends the author to
// look at file modes, which are not the problem.
//
// **Both boundaries, because the first attempt guarded only the second.** In a
// plain container the failure arrives at `clone`, not at `mount`: the process
// never starts, so a hint attached to the mount is written by code that never
// runs - verified by running these tests in an unprivileged container and
// reading what came out (E387).
//
// Empty for every other error, on the rule `startHint` already follows: a hint
// under every failure is a hint nobody reads.
func sysAdminHint(err error) string {
	if !errors.Is(err, syscall.EPERM) {
		return ""
	}

	return "\n  a mount namespace and a private /run both need CAP_SYS_ADMIN, which a" +
		"\n  container does not have by default - this is the usual answer when the" +
		"\n  build is itself running inside a container, and the outer step is where" +
		"\n  the capability has to come from"
}
