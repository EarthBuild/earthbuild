package exec

import (
	"time"

	"github.com/EarthBuild/earthbuild/engine/fstime"
)

// stamp is the time to write on a file this engine places on the host.
//
// The clamp itself lives in `engine/fstime`, because the guest needs the same
// rule and reads it from a request rather than from an environment it does not
// have (E549). One definition, so a build cannot be reproducible on one side of
// the sandbox boundary and not the other.
func stamp(actual time.Time) time.Time {
	at, ok := fstime.Clamp()
	if !ok {
		return actual
	}

	return at
}
