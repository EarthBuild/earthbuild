//go:build linux

package trace

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
)

// TestATracerCountsWhatItAnswered.
//
// **The number that decides whether pinning should be the default.** A traced
// path call costs 2.2µs when the stopped thread and the answering thread share
// a CPU and 45µs when they do not, and pinning both costs a four-way parallel
// step 2.9x (E681, E685). Which way that trade falls depends on how many calls
// a real build makes, and nothing reported it - so the argument has been
// conducted entirely on microbenchmarks.
//
// Counted rather than timed, which is also what a loaded machine allows.
func TestATracerCountsWhatItAnswered(t *testing.T) {
	SkipIfAlreadyFiltered(t)

	const rounds = 500

	done := make(chan int, 1)
	fail := make(chan error, 1)

	park := parking(t)

	go func() {
		runtime.LockOSThread()

		tr, err := StartOnSelf()
		if err != nil {
			fail <- err

			return
		}

		go tr.Run()

		<-tr.Servicing()

		// A child, because the filtering thread's own calls are recognised as
		// the engine's and answered without being counted as a step's.
		cmd := exec.Command(os.Args[0], "-test.run=^TestTheStatLoopHelper$")
		cmd.Env = append(os.Environ(), envStatLoop+"="+strconv.Itoa(rounds))

		_ = cmd.Run()

		done <- tr.Handled()

		park()
	}()

	select {
	case err := <-fail:
		t.Skipf("no seccomp user notification here: %v", err)
	case got := <-done:
		if got < rounds {
			t.Errorf("a child made at least %d traced calls and the tracer"+
				" counted %d", rounds, got)
		}

		t.Logf("%d notifications answered for %d deliberate calls", got, rounds)
	}
}
