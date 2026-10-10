package cli

import (
	"fmt"
	"io"
)

// warnSharedNet says that steps shared one network, and why.
//
// **Said because sharing is no longer what was asked for.** A step gets a
// network namespace of its own by default, so a build that shared one did not
// choose to: the guest had no `ip` or `iptables`, or setting one up failed. The
// build is correct either way - what is lost is that two steps wanting the same
// fixed port collide, and the loser waits out a connect timeout before
// reporting a daemon that would not answer (E923).
//
// Once per build, on the rule warnUnbounded and warnIncomplete already follow:
// every step of a build finds the same tool missing for the same reason.
//
// Names the setting, because a machine that shares deliberately should be able
// to stop being told. That is the difference between a warning and a nag.
func warnSharedNet(w io.Writer, reason string) {
	if w == nil || reason == "" {
		return
	}

	fmt.Fprintf(w,
		"warning: steps shared one network - %s\n"+
			"  two steps wanting the same port collide, and the loser waits out a\n"+
			"  connect timeout before reporting a daemon that would not answer\n"+
			"  set EARTH_STEP_NET=shared to ask for this and stop being told\n",
		reason)
}
