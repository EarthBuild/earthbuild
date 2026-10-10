//go:build linux

package trace_test

import (
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

	// **Never released, because releasing it destroys the thread.** The caller
	// locked this thread and installed a filter on it that cannot come off, so
	// a goroutine returning here is a thread the runtime *destroys* - and a
	// thread being torn down makes syscalls, which that filter traps. By
	// cleanup the tracer has usually stopped, so nobody answers them and the
	// thread stops in the kernel for good (E520, E521).
	//
	// That is where a `go test` timeout with no test named came from: the
	// cleanup blocks, and `--- FAIL` is printed only once cleanups finish, so
	// a test that had already failed its own deadline reported nothing at all.
	//
	// Parking until the process exits is what every call site already says it
	// wants - "the thread lives until the process does" - and a leaked thread
	// per filtered test costs a page of stack in a binary that is about to
	// exit.
	return func() { select {} }
}
