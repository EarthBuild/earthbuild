//go:build linux

package trace_test

import (
	"runtime"
	"testing"
)

// parking is the external-test copy of the helper in park_linux_test.go.
//
// Duplicated rather than exported: it exists only for tests, and an exported
// symbol on the package would be a public API for a problem the package does not
// have. Both copies are four lines and say the same thing, which is that a
// filtered thread must end with the test that filtered it (E627).
func parking(t *testing.T) func() {
	t.Helper()

	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	return func() {
		<-done

		runtime.Goexit()
	}
}
