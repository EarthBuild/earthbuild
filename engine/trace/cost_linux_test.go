//go:build linux

package trace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// What a traced path operation's *round trip* costs.
//
// **This measures the crossing and not the handler**, and the distinction is
// easy to lose: `StartOnSelf` records the filtering thread's tid, the work below
// runs on that same thread, and `handle` returns at the top for every
// notification it recognises as the engine's own. So no path is read, nothing is
// resolved and nothing is recorded. `strace -c` over four thousand traced calls
// shows nine `openat` in the whole run, where reading a path per call would need
// four thousand.
//
// That is the right isolation for one question - what does stopping a thread and
// answering it cost - and the wrong one for the obvious next question. On the
// same machine this reports 7.7µs and
// TestWhatATracedOperationCostsWithItsPathRead reports 14.6µs, so about half of
// a real traced call is work this never does (E681).
//
// The crossing is worth isolating because it is the part that moves: it is 2.2µs
// when the stopped thread and the answering thread share a CPU and 45µs when
// they do not, which under a hypervisor is the difference between a context
// switch and two vmexits.
//
// Reported rather than asserted against a threshold, because the ratio depends
// on the machine and a number baked in here would fail for being measured
// somewhere else. The bound that *is* asserted is loose enough to mean only one
// thing - that the loop has not stopped working - and tight enough to catch a
// tracer that has started doing something quadratic.
func TestWhatATracedOperationCosts(t *testing.T) {
	SkipIfAlreadyFiltered(t)

	dir := t.TempDir()

	// A file that exists and one that does not, since the loader's traffic is
	// mostly the second and a negative lookup resolves through the same path.
	present := filepath.Join(dir, "present.txt")

	err := os.WriteFile(present, []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	absent := filepath.Join(dir, "absent.txt")

	const rounds = 2000

	work := func() {
		var st unix.Stat_t

		for range rounds {
			_ = unix.Fstatat(unix.AT_FDCWD, present, &st, 0)
			_ = unix.Fstatat(unix.AT_FDCWD, absent, &st, 0)
		}
	}

	// Untraced, on this machine, now.
	start := time.Now()

	work()

	plain := time.Since(start)

	// Traced.
	out := make(chan time.Duration, 1)
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

		start := time.Now()

		work()

		out <- time.Since(start)

		park()
	}()

	var observed time.Duration

	select {
	case err := <-fail:
		t.Skipf("no seccomp user notification here: %v", err)
	case observed = <-out:
	case <-time.After(120 * time.Second):
		// Skipped, not failed. Four thousand traced calls take thirty
		// milliseconds on an idle machine; a box that cannot finish them in two
		// minutes is loaded, and that is not evidence about the tracer. Seen
		// once, while the same machine was compiling the rest of the suite.
		t.Skip("the traced work did not finish in two minutes;" +
			" this machine is too busy to measure on")
	}

	const calls = rounds * 2

	perPlain := plain / calls
	perObserved := observed / calls

	t.Logf("%d path calls: untraced %v (%v each), traced %v (%v each), %.0fx",
		calls, plain, perPlain, observed, perObserved,
		float64(observed)/float64(plain))

	// Loose, and it means one thing: a round trip through this engine is tens of
	// microseconds, so anything past a millisecond each is not overhead, it is a
	// tracer that has stopped working the way this one does.
	if perObserved > time.Millisecond {
		t.Errorf("a traced path call costs %v, which is not a round trip",
			perObserved)
	}
}
