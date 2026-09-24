package fstime

import (
	"os"
	"strconv"
	"time"
)

// SourceDateEpoch is the variable the reproducible-builds convention uses, and
// the one this engine takes its instruction from.
const SourceDateEpoch = "SOURCE_DATE_EPOCH"

// Clamp is the timestamp every file this build writes should carry, if any.
//
// Timestamps are a decision with a good case either way. A build that must be
// byte-reproducible wants them all pinned; a build handing its output to `make`
// or an incremental compiler wants them true, and an engine that picks one is
// wrong for the other half of its users. So it takes the instruction instead of
// making the choice, under the name the rest of the world already uses.
//
// Unset means preserve, which is what this engine did before the variable was
// read at all: nobody's output moves because this arrived.
//
// A value that is not a number is *not* a clamp and not an error here - the
// caller decides what to do about it - but it must not quietly become
// "preserve", because a misspelt variable means somebody asked for a
// reproducible build and would be handed a different one without being told.
//
// **Read on the host and nowhere else.** The guest is a different process in a
// different machine and does not have this variable; it was written to read one
// anyway, on the strength of a comment saying the value "reaches it as an
// environment variable forwarded at exec", and nothing forwarded it. So the
// clamp travels in the request that it applies to (E549), and a guest that
// consulted its own environment would be answering a question nobody asked it.
func Clamp() (time.Time, bool) {
	raw := os.Getenv(SourceDateEpoch)
	if raw == "" {
		return time.Time{}, false
	}

	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	return time.Unix(secs, 0), true
}

// Invented is the time a directory the build made up carries.
//
// **A directory nobody wrote has no time of its own**, and taking the wall clock
// is what made an unchanged COPY of unchanged bytes produce a different layer
// every build: identity includes mtimes (I8), so one invented ancestor re-keyed
// every step standing on it and no store that had to rebuild ever went warm
// again (E575, E576).
//
// The epoch rather than anything cleverer, because the only requirement is that
// two machines choose the same one. Pass it to Stamp, so a build with a clamp
// stamps these like everything else and a build without one still gets an
// answer that does not depend on when it ran.
var Invented = time.Unix(0, 0)

// Stamp is the time to write on a file: the clamp when there is one, and the
// file's own otherwise.
func Stamp(clamp *time.Time, actual time.Time) time.Time {
	if clamp != nil {
		return *clamp
	}

	return actual
}
