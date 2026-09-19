//go:build linux

package trace

import (
	"testing"
)

// parking prepares a way for a filtered thread to end with the test.
//
// **`select {}` held it for the life of the process.** A thread that installs a
// seccomp filter cannot remove it, so parking one forever leaves the filter in
// place long after the tracer that answers for it has stopped - nine of them,
// across this package's tests, all still filtered when the binary exits. That is
// why `SkipIfAlreadyFiltered` has to exist, and it is the best available
// explanation for `engine/trace` failing as a *package* in CI with every one of
// its tests passing (E627).
//
// Called from the test's own goroutine, which is the point of the two-step shape:
// `t.Cleanup` must be registered before the test can finish, and a worker
// goroutine calling it races the end of the test it is registering against.
//
// The returned function is the last statement of a goroutine that has locked its
// thread. It blocks until the test is over - so a reader loop behind it keeps
// answering while the assertions run - and then ends the goroutine, which is what
// destroys a locked thread and takes its filter with it. `runtime.Goexit` rather
// than a bare return so that anything added after it is a visible mistake instead
// of a silently immortal filter.
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
