package cli

import (
	"fmt"
	"io"
	"strings"
)

// deniedByPermission reports whether a mount failed for want of a capability.
//
// Matched on the errno's text because by here that is all there is: the reason
// crosses from the guest as a string, so the typed error is long gone. The two
// strings are Go's own renderings of EPERM and EACCES, fixed in the runtime and
// not localised, which makes them a duller handle than they look.
func deniedByPermission(reason string) bool {
	return strings.Contains(reason, "operation not permitted") ||
		strings.Contains(reason, "permission denied")
}

// warnIncomplete says that a step's filesystem was not fully built, and why.
//
// I11 is degrade-and-say-so, and these mounts had the "degrade" half right: a
// step that cannot have /sys is still a correct step, so `mountSys` and
// `mountCgroup2` carry on rather than refusing. The "say so" half did not
// exist - both call sites discarded the reason, so neither a CI log nor a local
// run could report whether either mount had succeeded. Answering that question
// meant building the engine and probing from inside a step, to learn something
// the guest already knew (E834a).
//
// Once per build, not per step, for the reason warnUnbounded is: every step
// fails the same mount for the same reason.
//
// Names the nested runtime specifically, because that is what reads this and
// what fails without it - and it fails a long way from the cause, as `runc run
// failed: no cgroup mount found in mountinfo` inside somebody else's build.
//
// **The fix line is conditional**, because this one warning carries every
// reason a step's filesystem came up short - a cgroups v1 machine, a /dev/pts
// that would not mount, a sandbox missing a feature - and root fixes exactly
// one of them. Advice beside a failure it cannot fix is worse than none: it
// costs a build to try and it spends the credit of the next line that offers
// some (E922).
//
// Not `-P`. `--allow-privileged` permits `RUN --privileged` in an Earthfile; it
// does not grant this process a capability it was not started with. The
// capability is named as well as the remedy, because `CAP_SYS_ADMIN` is the
// half that can be searched for.
func warnIncomplete(w io.Writer, reason string) {
	if w == nil || reason == "" {
		return
	}

	fmt.Fprintf(w,
		"warning: a step's filesystem was incomplete - %s\n"+
			"  nested runtimes (docker, podman, buildkit) cannot start;"+
			" other steps are unaffected\n",
		reason)

	if deniedByPermission(reason) {
		fmt.Fprint(w,
			"  fix: run as root - mounting a cgroup tree needs CAP_SYS_ADMIN\n")
	}
}
