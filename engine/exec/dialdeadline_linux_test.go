//go:build linux

package exec

import (
	"net"
	"strings"
	"testing"
	"time"
)

// A peer that accepts and never answers is a failure, not a wait.
//
// **The retry loop's deadline does not cover a blocked read.** Firecracker's
// multiplexer socket exists for as long as the VMM process does, so a guest
// that panicked leaves a socket that accepts connections and answers nothing:
// the dial succeeds, the greeting never arrives, and the read blocks with no
// deadline of its own. The build then hangs for ever rather than failing in
// thirty seconds - which is what happened to a guest handed an unformatted
// store device. It had already printed the diagnosis; nobody could see it.
func TestAGreetingThatNeverComesIsAFailure(t *testing.T) {
	t.Parallel()

	ours, theirs := net.Pipe()

	// The far side accepts and says nothing, which is the whole scenario.
	t.Cleanup(func() { _ = theirs.Close() })

	done := make(chan error, 1)

	go func() {
		_, err := readLine(ours)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a greeting that never came was read as one that did")
		}

		if !strings.Contains(err.Error(), "deadline") &&
			!strings.Contains(err.Error(), "timeout") {
			t.Errorf("the failure is not a timeout: %v", err)
		}

	case <-time.After(greetingPatience + 5*time.Second):
		t.Fatal("the read did not give up, so a build behind it never would")
	}
}
