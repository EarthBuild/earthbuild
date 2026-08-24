//go:build linux

package fleet_test

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// bytesRead is how many bytes this process has read through the read syscalls,
// which on Linux the kernel counts for us.
//
// `rchar`, not `read_bytes`: the latter counts what actually reached the block
// device, so a file still in page cache - which every file this suite writes and
// then reads is - reads as zero. What is being asked here is whether the engine
// *asked* for the bytes, not whether the disk had to supply them.
func bytesRead(t *testing.T) (uint64, bool) {
	t.Helper()

	b, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return 0, false
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		rest, found := strings.CutPrefix(line, "rchar: ")
		if !found {
			continue
		}

		n, convErr := strconv.ParseUint(strings.TrimSpace(rest), 10, 64)
		if convErr != nil {
			return 0, false
		}

		return n, true
	}

	return 0, false
}
