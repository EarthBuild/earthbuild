package exec

import (
	"os"
	"strconv"
	"time"
)

// sourceDateEpoch is the variable the reproducible-builds convention uses, and
// the one this engine takes its instruction from.
const sourceDateEpoch = "SOURCE_DATE_EPOCH"

// clampTime is the timestamp every file this build writes should carry, if any.
//
// Timestamps are a decision with a good case either way. A build that must be
// byte-reproducible wants them all pinned; a build handing its output to `make`
// or an incremental compiler wants them true, and an engine that picks one is
// wrong for the other half of its users. So it takes the instruction instead of
// making the choice, under the name the rest of the world already uses.
//
// Unset means preserve, which is what this engine already did: nobody's output
// moves because this arrived.
//
// A value that is not a number is *not* a clamp and not an error here - the
// caller decides what to do about it - but it must not quietly become
// "preserve", because a misspelt variable means somebody asked for a
// reproducible build and would be handed a different one without being told.
func clampTime() (time.Time, bool) {
	raw := os.Getenv(sourceDateEpoch)
	if raw == "" {
		return time.Time{}, false
	}

	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	return time.Unix(secs, 0), true
}

// stamp is the time to write on a file: the clamp when there is one, and the
// file's own otherwise.
func stamp(actual time.Time) time.Time {
	if at, ok := clampTime(); ok {
		return at
	}

	return actual
}
