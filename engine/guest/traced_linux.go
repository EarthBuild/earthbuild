//go:build linux

package guest

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/EarthBuild/earthbuild/engine/fdpass"
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
		// same CPU below.
		//
		// Pinning one without the other buys nothing, and that is measured
		// rather than argued: pinning only the answering thread and leaving the
		// step free ran 20k traced stats in 1.218s against 1.204s unpinned. The
		// step is the thread that has to be woken, and nothing pulls it to the
		// tracer's CPU (E685).
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

		// Closed when the step is over, so the goroutine below can tell a
		// tracer that outlived its step from one that stopped underneath it.
		finished := make(chan struct{})

		reading := supervise(tr, release, finished, cpu, pinning)

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

		// **What the round trips were paid for.** A traced path call costs
		// 2.2µs when the stopped thread and the answering thread share a CPU
		// and 45µs when they do not, and pinning both costs a four-way parallel
		// step 2.9x (E681, E685). Which way that trade falls depends on how many
		// calls a real build makes, and the argument has been conducted entirely
		// on microbenchmarks because nothing counted them.
		//
		// Only when asked: it is one line per step and a build has many.
		if os.Getenv(timing.Env) != "" {
			fmt.Fprintf(os.Stderr, "earth: traced %d path calls\n", tr.Handled())
		}

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

// supervise runs the tracer's notification loop and guarantees the step is let
// go if that loop stops early.
//
// Shared by both arrangements - the filter installed in the guest before the
// clone, and the filter installed by the shim and sent back - because what it
// guards is the same either way and is the most expensive lesson in this
// package: a tracer that stops while its step is still filtered leaves that
// step's next intercepted syscall stopped in the kernel with nothing coming to
// answer it (E520, E582).
//
// The returned channel closes when the loop is done, so a caller can wait for it
// before taking sightings. `finished` is the caller's promise that the step is
// already over and nothing needs releasing.
func supervise(
	tr *trace.Tracer, release func(), finished <-chan struct{}, cpu int, pinning bool,
) <-chan struct{} {
	reading := make(chan struct{})

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

	return reading
}

// stepResult is what a step left behind: its combined output and how it ended.
type stepResult struct {
	out []byte
	err error
}

// listenerWait is how long the guest waits for the shim's seccomp listener.
//
// **A deadline, because the failure that matters does not fail.** A shim that
// cannot install a filter closes the channel and the wait ends at once; a shim
// that dies between the install and the send sends nothing at all, and a read
// without a deadline then waits for a descriptor that is not coming - which is
// a build that hangs rather than one that says what happened (E587, E607).
//
// Generous against the work involved, which is a `seccomp` call and a `sendmsg`.
const listenerWait = 10 * time.Second

// runObservedViaShim runs a step that installs its own filter and sends it back.
//
// **The arrangement that does not deadlock.** The guest starts the shim with no
// filter anywhere, so the `CLONE_VFORK` in `os/exec` is released by the shim's
// own exec instead of waiting on an `execve` that has trapped; the shim then
// installs the filter on the thread that becomes the step and hands the listener
// over this channel. By the time anything traps, the guest is an ordinary
// process that can answer (E723, E729, E730).
//
// Two things fall out of it rather than being arranged. The tracer no longer has
// to disregard a thread of its own, because no thread of the guest's is filtered
// and the engine's own opens can no longer be recorded as the step's (E211). And
// `release` works at the moment it is needed: the shim has exec'd, so
// `cmd.Process` is filled in, where a step stopped at its first `execve` had no
// process to cancel (E673).
func runObservedViaShim(
	fn func(channel *os.File) ([]byte, error), fill func(string) error, release func(),
) ([]byte, trace.Sightings, error) {
	here, there, err := fdpass.SocketPair()
	if err != nil {
		out, runErr := fn(nil)

		return out, trace.Unobserved(fmt.Errorf("make a channel for the step's listener: %w", err)), runErr
	}

	defer func() { _ = here.Close() }()

	channel, err := there.File()
	if err != nil {
		out, runErr := fn(nil)

		return out, trace.Unobserved(fmt.Errorf("name the step's end of the channel: %w", err)), runErr
	}

	defer func() { _ = channel.Close(); _ = there.Close() }()

	done := make(chan stepResult, 1)
	// Closed when the step is over, so the supervisor can tell a tracer that
	// outlived its step from one that stopped underneath it.
	finished := make(chan struct{})

	// **Started before the listener arrives, because the listener comes from
	// it.** The step blocks at its own `execve` until somebody answers, and
	// that is a wait rather than a deadlock now: nothing here is inside a
	// clone, so the goroutine that answers can always be scheduled.
	go func() {
		out, runErr := fn(channel)

		close(finished)

		done <- stepResult{out: out, err: runErr}
	}()

	err = here.SetReadDeadline(time.Now().Add(listenerWait))
	if err != nil {
		return finishUnobserved(done, fmt.Errorf("set a deadline on the listener channel: %w", err))
	}

	listener, err := fdpass.RecvFile(here)
	if err != nil {
		// The shim closed the channel or died. Either way this step runs
		// untraced, which costs it the tier and nothing else.
		return finishUnobserved(done, fmt.Errorf("the step sent no syscall listener: %w", err))
	}

	// Cleared, or every later read on this connection inherits it.
	err = here.SetReadDeadline(time.Time{})
	if err != nil {
		return finishUnobserved(done, fmt.Errorf("clear the listener deadline: %w", err))
	}

	// Owned, not borrowed: a tracer holding only the number loses the listener
	// to a finaliser (E215).
	tr := trace.FromListener(listener)
	tr.Fill = fill

	// **Both ends, or neither** - the same trade as the arrangement this
	// replaces. The step is pinned by the shim, which is the only thing left
	// that shares a thread with it; the loop that answers it is pinned here, to
	// the same CPU. Pinning one and not the other buys nothing and was measured
	// not to (E685).
	cpu, pinning := pinChoice()

	reading := supervise(tr, release, finished, cpu, pinning)

	r := <-done

	if os.Getenv(timing.Env) != "" {
		fmt.Fprintf(os.Stderr, "earth: traced %d path calls\n", tr.Handled())
	}

	runErr := r.err

	// A file this engine could not obtain fails the step, and it has to be
	// checked here because the step itself cannot tell: it asked, was handed
	// "no such file", and took the other branch (E289).
	unfilled := tr.Unfilled()
	if unfilled != nil && runErr == nil {
		runErr = unfilled
	}

	// **A hang-up is how a step of this kind ends, not how it fails.** When the
	// guest installed the filter, a thread of the guest's carried it and the
	// listener stayed open for as long as that thread lived - so POLLHUP meant
	// trouble (E520, E521). Here the step is the only thing carrying the filter,
	// so the listener hangs up the moment the step exits, which is every step.
	//
	// Nor can it hide the hazard those experiments describe: that one needs a
	// filtered task still running, and POLLHUP is the kernel saying there is
	// none. Any other reason for stopping is still the step's business.
	stopped := tr.Stopped()
	if tr.HungUp() {
		stopped = nil
	}

	if stopped != nil && runErr == nil {
		runErr = fmt.Errorf("this step's syscall tracer stopped while it was"+
			" running: %w\n  anything the step did after that is stopped in"+
			" the kernel, so the step cannot be trusted to have finished", stopped)
	}

	seen := tr.Sightings()

	_ = tr.Close()
	<-reading

	return r.out, seen, runErr
}

// finishUnobserved waits for a step that is running without a tracer and reports
// why it has no observation.
//
// Separate because the alternative is four copies of the same three lines, and
// the thing they must all get right is that the step is **waited for**: it is
// already running, and returning without it would leave a live process behind
// and report an exit nobody had.
func finishUnobserved(done <-chan stepResult, why error) ([]byte, trace.Sightings, error) {
	r := <-done

	return r.out, trace.Unobserved(why), r.err
}
