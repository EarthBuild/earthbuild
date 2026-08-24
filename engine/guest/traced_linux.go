//go:build linux

package guest

import (
	"fmt"
	"runtime"

	"github.com/EarthBuild/earthbuild/engine/trace"
)

// runObserved runs a step on a thread whose syscalls are watched.
//
// The arrangement, and every part of it is load-bearing (E206, E211):
//
//   - a goroutine of its own, which **locks its thread and never unlocks it**.
//     A seccomp filter cannot be removed, so the thread has to be destroyed
//     rather than returned to the scheduler, and a goroutine exiting while
//     locked is what destroys it. Filters accumulate, so the thread cannot be
//     reused for a second step either.
//   - the step is started from that same thread. A filter is inherited across
//     fork and carried through exec, so the step is traced with no helper
//     binary - which matters, because `SysProcAttr.Chroot` would require any
//     helper to exist inside the step's own filesystem.
//   - the tracer disregards that thread's own syscalls. `exec.Cmd` opens
//     /dev/null in the *parent* for a nil Stdout, on that very thread.
//
// A step whose filter could not be installed still runs. Tracing is how a step
// earns an L2 hit, not how it is allowed to execute, so failing here costs the
// tier and nothing else - and says so, rather than returning an observation that
// looks complete (I3, I11).
func runObserved(
	fn func() ([]byte, error), fill func(string) error, release func(),
) ([]byte, trace.Sightings, error) {
	type result struct {
		out  []byte
		err  error
		seen trace.Sightings
	}

	done := make(chan result, 1)

	go func() {
		runtime.LockOSThread()

		tr, err := trace.StartOnSelf()
		if err != nil {
			// No tracer, so the step runs unobserved and the observation says
			// so. Nothing is unlocked here either: a failed install may still
			// have set no-new-privs, and the thread is cheap to lose.
			out, runErr := fn()
			done <- result{out: out, err: runErr, seen: trace.Unobserved(err)}

			return
		}

		// Lazy materialisation, when this guest has somewhere to ask (E296).
		// Nil for every build today, and a nil one leaves the tracer watching
		// rather than filling.
		tr.Fill = fill

		reading := make(chan struct{})
		// Closed when the step is over, so the goroutine below can tell a
		// tracer that outlived its step from one that stopped underneath it.
		finished := make(chan struct{})

		go func() {
			tr.Run()
			close(reading)

			// **The step has to be let go, or nothing below ever runs.** A
			// tracer that stops while its step is still filtered leaves that
			// step's next intercepted syscall stopped in the kernel with
			// nothing coming to answer it. The report for exactly this is
			// twenty lines further down (E520) - and it is downstream of
			// `fn()`, which is the one thing a wedged step never does.
			//
			// Measured: `+all-binaries` sat for thirty minutes on a `printf`,
			// the step blocked in `seccomp_do_user_notification` and the guest
			// in `__futex_wait`, with the machine otherwise idle. The diagnosis
			// was already written and could not be reached (E582).
			if tr.Stopped() == nil || release == nil {
				return
			}

			select {
			case <-finished:
				// Over already: whatever the tracer thinks, nothing is waiting.
			default:
				release()
			}
		}()

		out, runErr := fn()

		close(finished)

		// **A file this engine could not obtain fails the step**, and it has to
		// be checked here because the step itself cannot tell: it asked for a
		// file, was handed "no such file", and took the other branch (E289).
		//
		// Only when a fill was configured at all - a tracer that only watches
		// never sets this, which is every use today.
		unfilled := tr.Unfilled()
		if unfilled != nil && runErr == nil {
			runErr = unfilled
		}

		// **A servicer that stopped early is the step's business.** The filter
		// outlives it, so anything the step does afterwards is stopped in the
		// kernel with nothing coming to release it. Until this was reported, a
		// build in that state hung with no message on either side (E520).
		stopped := tr.Stopped()
		if stopped != nil && runErr == nil {
			runErr = fmt.Errorf("this step's syscall tracer stopped while it was"+
				" running: %w\n  anything the step did after that is stopped in"+
				" the kernel, so the step cannot be trusted to have finished", stopped)
		}

		// Read before the listener closes. The step is reaped, so every
		// notification it made has already been answered and none is queued -
		// but this goroutine is still filtered, and taking the sightings while
		// something is still answering means an allocation here cannot stall.
		seen := tr.Sightings()

		_ = tr.Close()
		<-reading

		done <- result{out: out, err: runErr, seen: seen}

		// Returns, so the runtime destroys this thread and the filter with it.
	}()

	r := <-done

	return r.out, r.seen, r.err
}
