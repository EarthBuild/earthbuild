package cli

import (
	"fmt"
	"io"
)

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
func warnIncomplete(w io.Writer, reason string) {
	if w == nil || reason == "" {
		return
	}

	fmt.Fprintf(w,
		"warning: a step's filesystem was incomplete - %s\n"+
			"  steps ran, and anything reading only its own files is unaffected\n"+
			"  what breaks is a nested runtime - docker, podman or buildkit started\n"+
			"  inside a step - which needs /sys and a cgroup tree to start a container\n",
		reason)
}
