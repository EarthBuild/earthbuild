//go:build linux

package trace

import (
	"runtime"
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

	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	return func() {
		<-done

		runtime.Goexit()
	}
}
