package cli

import (
	"fmt"
	"io"
)

// warnUnbounded says that a build's steps ran without the resource limits they
// were given, and why.
//
// I11 is degrade-and-say-so, and the guest had the "degrade" half exactly
// right: an unbounded step still runs, because a memory ceiling is not a
// correctness property and refusing the build would be worse. The "say so"
// half was a line on the guest's stderr after `Serve` returned - after the
// build, in a stream the host does not relay (E123).
//
// Once per build, not per step. Every step degrades for the same reason, and a
// warning printed forty times is a warning nobody reads, which is where not
// printing it ends up too.
//
// A warning rather than a refusal, on the same grounds as the case-insensitive
// store note beside it: the build is correct either way, and what is lost is a
// bound - a step that would have been stopped at its ceiling takes the machine
// down with it instead.
func warnUnbounded(w io.Writer, reason string) {
	if w == nil || reason == "" {
		return
	}

	fmt.Fprintf(w,
		"warning: steps ran unbounded - %s\n"+
			"  a memory or process ceiling was asked for and could not be applied,\n"+
			"  so a step that would have been stopped at its limit will instead take\n"+
			"  as much of this machine as it asks for\n"+
			"  rootless: cgroup v2 delegates a writable subtree to a user session, but a\n"+
			"  process can only be moved into one it was started inside - `systemd-run\n"+
			"  --user --scope` is how a rootless runtime gets one\n", reason)
}
