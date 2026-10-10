//go:build linux

package trace

import (
	"testing"
	"time"
)

// TestATracerSaysWhenItIsListening.
//
// **A filtered step must not start before somebody is answering it.**
// `StartOnSelf` installs the filter and returns; the loop starts afterwards, on
// a goroutine, and until it reaches its poll nothing will answer a notification.
// A step launched in that window whose first `execve` traps waits for a
// supervisor that has not begun - and E673 caught exactly that: a child stopped
// at `syscall_trace_enter` with a guest thread in `D` inside `kernel_clone`,
// which is what starting a thread looks like when it cannot finish.
//
// The signal is what closes the window. It has to be raised *before* the poll
// rather than after it returns: a caller waiting on it wants to know somebody is
// listening, and "the poll came back" is a different and later fact.
func TestATracerSaysWhenItIsListening(t *testing.T) {
	t.Parallel()

	tr := NewTracer(-1)

	select {
	case <-tr.Servicing():
		t.Fatal("a tracer that has not been run claims to be listening")
	default:
	}

	go tr.Run()

	select {
	case <-tr.Servicing():
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never said it was listening, so a step waiting on this" +
			"\n  would be refused rather than run - which is the safe answer and" +
			"\n  still means the signal is broken")
	}

	_ = tr.Close()
}

// TestSayingSoTwiceIsNotAPanic: `waitForWork` is a loop and the announcement is
// inside it, so it is reached on every pass. Closing a channel twice is a panic,
// which is a poor way to find out.
func TestSayingSoTwiceIsNotAPanic(t *testing.T) {
	t.Parallel()

	tr := NewTracer(-1)

	for range 3 {
		tr.serviceOnce.Do(func() { close(tr.servicing) })
	}

	select {
	case <-tr.Servicing():
	default:
		t.Fatal("the signal was not raised")
	}
}
