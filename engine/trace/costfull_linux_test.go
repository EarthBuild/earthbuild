//go:build linux

package trace

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// envStatLoop turns this binary into the thing being measured rather than the
// thing measuring. A re-exec of the test binary, because it is the only program
// certainly on this machine that can be told to make exactly N path calls and
// nothing else - `sh` would add its own, and how many is a property of which
// `sh` it is.
const envStatLoop = "EARTH_TRACE_STAT_LOOP"

// What a traced path call costs *including* reading the path.
//
// **TestWhatATracedOperationCosts measures the round trip and not the handler.**
// `StartOnSelf` records the filtering thread's tid, that test works on that same
// thread, and `handle` returns at the top for every notification it recognises
// as the engine's own - so no path is read, nothing is resolved and nothing is
// recorded. `strace -c` over four thousand traced calls shows nine `openat` in
// the whole run, where reading a path per call would need four thousand (E681).
//
// The difference is not small: 2.2µs for the crossing against 8.5µs measured
// through the engine, so most of a traced call is work that test never does.
//
// A *child* is what production traces - a step is forked from the filtered
// thread and inherits the filter across exec (E211) - and a child has a pid of
// its own, so nothing about it is mistaken for the engine. Timed against the
// same child run untraced, which cancels the cost of starting it.
func TestWhatATracedOperationCostsWithItsPathRead(t *testing.T) {
	SkipIfAlreadyFiltered(t)

	const rounds = 4000

	child := func() *exec.Cmd {
		c := exec.CommandContext(t.Context(), os.Args[0])
		c.Env = append(os.Environ(), envStatLoop+"="+strconv.Itoa(rounds))

		return c
	}

	// Untraced, on this machine, now. Twice: the first pays for a cold binary
	// in the page cache, and that is not what is being compared.
	err := child().Run()
	if err != nil {
		t.Fatalf("the helper does not run: %v", err)
	}

	start := time.Now()

	err = child().Run()
	if err != nil {
		t.Fatalf("the helper does not run: %v", err)
	}

	plain := time.Since(start)

	// Traced, and started from the thread carrying the filter.
	out := make(chan time.Duration, 1)
	fail := make(chan error, 1)

	park := parking(t)

	go func() {
		runtime.LockOSThread()

		tr, serr := StartOnSelf()
		if serr != nil {
			fail <- serr

			return
		}

		go tr.Run()

		<-tr.Servicing()

		began := time.Now()

		rerr := child().Run()

		out <- time.Since(began)

		if rerr != nil {
			fail <- rerr
		}

		park()
	}()

	var observed time.Duration

	select {
	case err = <-fail:
		t.Skipf("no seccomp user notification here: %v", err)
	case observed = <-out:
	case <-time.After(120 * time.Second):
		t.Skip("the traced child did not finish in two minutes;" +
			" this machine is too busy to measure on")
	}

	if observed <= plain {
		t.Errorf("a traced child took %v against %v untraced, so either nothing"+
			"\n  was traced or the two runs are not comparable", observed, plain)

		return
	}

	per := (observed - plain) / rounds

	t.Logf("%d path calls in a traced child: untraced %v, traced %v, %v each",
		rounds, plain, observed, per)

	// The same loose bound the round-trip measurement carries, and it means the
	// same thing: tens of microseconds is a round trip and a handler, and a
	// millisecond is neither.
	if per > time.Millisecond {
		t.Errorf("a traced path call costs %v, which is not a round trip", per)
	}
}
