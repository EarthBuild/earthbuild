//go:build !linux

package fleet_test

import "testing"

// bytesRead has no answer away from Linux, where nothing counts a process's
// reads for it. The tests that need one say so and skip.
func bytesRead(t *testing.T) (uint64, bool) {
	t.Helper()

	return 0, false
}
