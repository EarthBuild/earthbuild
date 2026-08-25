//go:build linux

package guest

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/EarthBuild/earthbuild/engine/timing"
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

		// **Both ends of the round trip, or neither.** The step inherits this
		// thread's affinity across fork exactly as it inherits its filter, so
		// pinning here pins the step; the loop that answers it is pinned to the
		// same CPU below. Pinning one without the other buys nothing - the
		// wakeup still crosses vCPUs, which is the whole cost (E681).
		cpu, pinning := pinChoice()
		if pinning {
			// Best effort: a step whose thread could not be pinned runs at the
			// speed it ran at before this existed, which is not a failure worth
			// refusing a build over.
			_ = trace.Pin(cpu)
		}

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
			if pinning {
				// Locked and never unlocked, because an affinity left on a
				// thread handed back to the scheduler is inherited by whatever
				// runs there next. This goroutine ends when the tracer does,
				// and a locked goroutine ending destroys its thread.
				//
				// Before `tr.Run()` rather than inside it: the step does not
				// start until this loop is servicing, so the thread this needs
				// is created while nothing is filtered - which is the window
				// E673 says must stay clear.
				runtime.LockOSThread()

				_ = trace.Pin(cpu)
			}

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
			if tr.Stopped() == nil {
				return
			}

			select {
			case <-finished:
				// Over already: whatever the tracer thinks, nothing is waiting.
			default:
				// **Close the listener first, and this is the release that
				// works.** `release` cancels the step's *process*, and
				// `os/exec` fills `cmd.Process` in only once the child has
				// execed - which is exactly what a child stopped at its first
				// intercepted `execve` has not done. So at the one moment this
				// matters there is a process and nothing to cancel, and the
				// guard on `cmd.Process` returns having done nothing (E673).
				//
				// Closing the notification descriptor does not need to know the
				// pid: the kernel fails every syscall blocked on it with ENOSYS
				// (`seccomp_unotify(2)`). The step then fails, saying so,
				// instead of waiting for a supervisor that has gone.
				_ = tr.Close()

				if release != nil {
					release()
				}
			}
		}()

		// **A filtered step must not start before somebody is answering.**
		// `StartOnSelf` installs the filter and returns; the loop above starts
		// on a goroutine and is not listening until it reaches its poll. A step
		// launched inside that window whose first `execve` traps waits for a
		// supervisor that has not begun - and if beginning needs a thread, and
		// creating a thread needs the `clone` the trapped step is holding up,
		// neither side moves again. That is E673's capture: a child stopped at
		// `syscall_trace_enter` and a guest thread in `D` inside `kernel_clone`.
		//
		// Waited for rather than assumed. The bound is generous because the
		// only thing on the other side of it is a goroutine reaching a poll;
		// if that has not happened in a second, something is wrong that waiting
		// longer will not mend.
		// Timed, because "the window is closed" and "the window was never open"
		// look identical from a build that worked. A wait that is always
		// instant says the race was theoretical here; one that is sometimes
		// milliseconds says it was not.
		endWait := timing.Phase("guest:tracer-wait", "")

		// A timer that is stopped rather than `time.After`, which holds its
		// channel until it fires: every traced step would otherwise leave one
		// alive for a second, and a build is a great many steps.
		late := time.NewTimer(serviceWait)

		select {
		case <-tr.Servicing():
			late.Stop()
			endWait()
		case <-late.C:
			endWait()

			// The filter is already on this thread and cannot be taken off, so
			// the step cannot be run unobserved instead. Closing the listener
			// makes every syscall it would have trapped fail with ENOSYS, which
			// turns a build that hangs into one that says what happened.
			_ = tr.Close()

			done <- result{err: fmt.Errorf(
				"this step's syscall tracer did not start listening within %s"+
					"\n  the filter is installed and nothing would answer it, so the"+
					"\n  step was refused rather than left stopped in the kernel",
				serviceWait), seen: trace.Unobserved(errNotServicing)}

			return
		}

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

// serviceWait is how long a step waits for its tracer to start listening.
//
// Generous, because the only thing on the other side is a goroutine reaching a
// poll. It is a deadlock detector rather than a timeout: a second is far longer
// than starting takes and far shorter than the minutes a wedged step used to
// cost (E673).
const serviceWait = time.Second

// errNotServicing marks an observation as incomplete when the tracer never
// began. The step did not run, so nothing was observed, and saying so keeps it
// out of L2 rather than letting an empty observation look like a complete one.
var errNotServicing = errors.New("the syscall tracer did not start listening")

// pinChoice is the CPU this step and its tracer should share, if they should.
//
// **Rotated rather than fixed.** A guest serves more than one step at a time,
// and sending every one of them to CPU 0 would trade a 19x saving on the round
// trip for a queue on one vCPU. Rotating costs nothing and means two concurrent
// steps collide only when there are more steps than CPUs.
func pinChoice() (int, bool) {
	if os.Getenv(EnvTracePin) == "" {
		return 0, false
	}

	n := runtime.NumCPU()

	// One CPU is already the pinned arrangement, and Pin would refuse a machine
	// it cannot confine a thread on anyway.
	if n < 2 {
		return 0, false
	}

	return int(pinTurn.Add(1)-1) % n, true
}

// pinTurn is where the rotation has got to. Unsynchronised arithmetic would let
// two steps read the same turn and pick the same CPU, which is the one thing
// rotating exists to avoid.
var pinTurn atomic.Uint64
