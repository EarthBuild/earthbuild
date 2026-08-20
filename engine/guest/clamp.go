package guest

import (
	"os"
	"strconv"
	"time"
)

// stamp is the time to write on a file the guest copies.
//
// The same rule as the host's, and deliberately a second small copy rather than
// a shared package: the guest is a different binary in a different machine, and
// the value reaches it as an environment variable forwarded at exec. Sharing
// eight lines across that boundary would buy nothing and cost a dependency.
//
// See engine/exec/clamp.go for why the engine takes an instruction here rather
// than choosing: pinning timestamps and keeping them true are both right, for
// different builds.
func stamp(actual time.Time) time.Time {
	raw := os.Getenv("SOURCE_DATE_EPOCH")
	if raw == "" {
		return actual
	}

	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return actual
	}

	return time.Unix(secs, 0)
}
