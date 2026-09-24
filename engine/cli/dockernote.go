package cli

import (
	"fmt"
	"io"
)

// warnNoDockerClient says that a WITH DOCKER step was given a daemon and no
// client, and what that will look like.
//
// E145 made an unusable host client non-fatal, which is right - an image can
// carry its own, and the daemon is what no image can supply. It leaves the case
// where the image carries none, and what the step prints then is
//
//	/bin/sh: docker: not found
//
// about a socket that is mounted and working. That is the message E117 existed
// to remove, arriving through the door E145 opened: **making a refusal into a
// degradation moves the confusion from the engine to the step.**
//
// So the engine says it first, once, at the point it knows - which is mount
// time, long before the step runs. I11: degrade if you must, and say so.
func warnNoDockerClient(w io.Writer, reason string) {
	if w == nil || reason == "" {
		return
	}

	fmt.Fprintf(w,
		"warning: WITH DOCKER got a daemon and no client - %s\n"+
			"  the socket is mounted and the daemon is reachable, so a step whose image\n"+
			"  carries its own client works: `RUN apk add --no-cache docker-cli`, or the\n"+
			"  equivalent for that image\n"+
			"  a step whose image has none will say `docker: not found` about a file that\n"+
			"  is genuinely absent, rather than about the mount\n", reason)
}
