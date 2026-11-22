//go:build !linux

package guest

import (
	"os"
	"time"
)

// usageOf reads what a finished process spent.
//
// Off Linux this reports the CPU and no memory: `ru_maxrss` is in bytes on
// darwin and kilobytes on Linux, and rather than encode that difference in a
// file that cannot be run here, the number this platform cannot state honestly
// is left at zero (E467).
func usageOf(st *os.ProcessState) (cpu time.Duration, maxRSS uint64) {
	if st == nil {
		return 0, 0
	}

	return st.UserTime() + st.SystemTime(), 0
}
