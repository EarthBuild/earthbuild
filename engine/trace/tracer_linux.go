//go:build linux

package trace

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// Sightings is what a step was seen to look at.
//
// Paths as the *step* named them, resolved to absolute but not translated out of
// whatever root it was running in - that translation needs the mount, which this
// package does not have and should not.
//
// **No digests, and no division into read and absent.** A notification says a
// path was named, and nothing about how the call came out: the answer is sent
// before the syscall runs, which is what lets every one of them proceed. Whether
// a path was there is decided later, against the base, exactly as it is for a
// copy's destination (engine/guest/observe.go) - a path present in the mount
// becomes a read, one absent from it becomes a negative lookup, and both come
// from the same list.
type Sightings struct {
	// Paths is sorted and deduplicated, so that two runs seeing the same things
	// in a different order produce the same value (I12).
	Paths []string
	// Opened is the subset of Paths the step opened rather than interrogated.
	//
	// The distinction is only interesting for a directory. Opening one is how a
	// step enumerates it, so its *contents* decide what the step does and belong
	// in the key; stat'ing one is how a step walks past on the way to a file
	// inside, and keying that on the directory's contents makes a sibling
	// appearing invalidate a step that never looked at it.
	//
	// Sorted for Paths' reason (I12).
	Opened []string
	// Incomplete says this engine knows it missed something. A step whose
	// sightings are incomplete can still be built and still be cached; what it
	// cannot do is serve an L2 hit, because the reads it did not see are exactly
	// the ones that would make that hit wrong (I3).
	Incomplete bool
	// Why names each distinct reason, sorted. A step that silently never earns
	// an L2 hit is a performance bug nobody can find; this is what turns it into
	// a sentence. It also makes the reasons *distinguishable* to a test, which
	// is how the architecture check was found to be untested: without a reason,
	// every way of losing an observation looks identical from outside (E209).
	Why []string
}

// Reasons an observation is incomplete.
const (
	whyForeignArch = "a syscall in another architecture's numbering"
	whyUnknownCall = "a trapped syscall this engine reads no path from"
	whyUnreadable  = "a path argument that could not be read"
)

// Tracer answers notifications and remembers the paths they carried.
//
// One per step. The loop must keep answering whatever happens - a step whose
// notification goes unanswered is stopped in the kernel for ever - so every path
// through it ends in a response, and the interesting decisions are all about
// what to *record* rather than whether to reply.
type Tracer struct {
	// Report is where a stop is announced, as it happens. Nil means stderr.
	//
	// **Announced, not just recorded.** A tracer that stops early leaves the
	// step stopped in the kernel for ever, so the reason is wanted while the
	// build is *still hung* - and reporting it through the step's error, as this
	// used to, delivers it only when the step returns, which is exactly what a
	// hung step never does. Three captures came back with an empty verdict for
	// that reason (E522).
	Report io.Writer
	fd     int
	// listener owns the descriptor when the tracer made it itself.
	//
	// An `*os.File` closes its descriptor from a **finaliser**, so a tracer
	// holding only `fd` has the listener closed the moment the file becomes
	// unreachable - and the number is then handed out again, so `Close` closes
	// whatever got it. It presented as `readdirent …: bad file descriptor` in an
	// unrelated capture, four runs in five (E215).
	//
	// Nil when the descriptor came from somewhere else, as it does for the
	// helper arrangement, where whoever passed it owns it.
	listener *os.File
	// stopR and stopW wake a blocked Run.
	//
	// A descriptor cannot be used for this by closing it: `receive` blocks in an
	// `ioctl`, and **closing a descriptor does not wake a thread already inside
	// one**. So Run waits on the listener *and* on stopR, and Close closes stopW
	// - which is a readable event on stopR and returns from the wait at once.
	//
	// The lesson was already written down, in E206, in the comment on a test
	// that consequently never joined this loop. Then the guest joined it and
	// every traced step hung (E214). A mechanism beats a note.
	stopR, stopW int
	// closing separates a stop that was asked for from one that was not.
	//
	// Without it both arrive at Run as the same readable event on stopR, so a
	// stop pipe that fires on its own is indistinguishable from an orderly
	// Close - and the step it strands hangs with nothing said (E522).
	closing atomic.Bool
	// closed guards the descriptor against a second Close; see Close.
	closed atomic.Bool
	// servicing is closed once the loop is actually waiting for notifications.
	//
	// **The window between installing a filter and servicing it is a deadlock
	// waiting to happen.** `StartOnSelf` installs the filter and returns; the
	// loop starts afterwards, on a goroutine, and until it reaches its poll
	// nothing will answer. A step launched in that window whose first `execve`
	// traps waits for a supervisor that has not started - and if starting it
	// needs a thread, and creating a thread needs the `clone` that the trapped
	// step is holding up, neither side moves again (E673).
	servicing   chan struct{}
	serviceOnce sync.Once
	// mine is the engine's own thread, or zero when the engine has none.
	//
	// The filter lives on the thread that installed it, so that thread's
	// syscalls trap alongside the step's - and that thread belongs to the
	// engine. `exec.Cmd` with a nil Stdout opens /dev/null in the *parent*, on
	// that very thread, so without this the plainest use of the tracer
	// attributes /dev/null to every step that does not redirect its output.
	//
	// A **thread** id, and that is not a detail: `seccomp_notif.pid` is what the
	// kernel calls `task_pid_vnr`, which for a thread is its tid rather than the
	// process it belongs to. Comparing against `os.Getpid()` matches nothing
	// that any non-main thread does, which is every notification this is meant
	// to catch (E211).
	mine uint32

	// handled counts the notifications this loop answered.
	//
	// **The number the pinning argument needs and never had.** A traced path
	// call costs 2.2µs sharing a CPU and 45µs not, and pinning costs a
	// four-way parallel step 2.9x (E681, E685) - which way that falls depends
	// on how many calls a real build makes, and nothing counted them.
	//
	// Atomic because it is read from whoever is waiting on the step, and the
	// loop that increments it is a different goroutine (E689).
	handled atomic.Int64

	// hungUp records that Run stopped on POLLHUP rather than for another
	// reason. See Tracer.HungUp for why the caller, not the tracer, decides
	// whether that is a failure.
	hungUp atomic.Bool

	// soleCarrier says the step is the only thing carrying this filter, which
	// is true when a shim installed it and sent the listener back. Then a
	// hang-up is the step exiting rather than a servicer abandoning a filtered
	// thread, and is not worth a line in the log. Set by FromListener, which is
	// the only way that arrangement builds a tracer.
	soleCarrier bool

	// mem is `/proc/<pid>/mem` kept open for whichever process was asked about
	// last, saving the open and close that were two thirds of the handler
	// (E681). Touched only from the notification loop, which is one goroutine.
	mem memFiles

	// Fill fetches a path the step is about to open and this machine does not
	// have, before the syscall is allowed to proceed.
	//
	// Nil for a tracer that only watches, which is what every observation-only
	// use wants. Set, it turns the tracer into a lazy materialiser: the step is
	// stopped in the kernel, the file arrives, and the open then finds it
	// (E289).
	//
	// **Succeeding without creating anything means the file is genuinely
	// absent**, and the syscall proceeds to its honest ENOENT. Returning an
	// error means this engine could not obtain a file that may well exist, which
	// is recorded as fatal - see Unfilled.
	Fill func(path string) error

	// stopErr is why Run gave up, if it did. See stopped.
	stopMu  sync.Mutex
	stopErr error

	mu       sync.Mutex
	paths    map[string]bool
	// opened is the subset of paths the step opened rather than interrogated.
	opened   map[string]bool
	why      map[string]bool
	unfilled error
}

// NewTracer takes ownership of a listener returned by install.
func NewTracer(fd int) *Tracer {
	t := &Tracer{
		fd: fd, stopR: -1, stopW: -1,
		paths: map[string]bool{}, why: map[string]bool{},
		servicing: make(chan struct{}),
	}

	var p [2]int

	// A pipe rather than a poll timeout: an interval is a choice between waking
	// up for nothing and taking that long to stop, and there is no need to make
	// it. A failure here leaves Run relying on the listener alone, which is the
	// behaviour without this and still terminates when the last filtered process
	// is gone.
	err := unix.Pipe2(p[:], unix.O_CLOEXEC)
	if err == nil {
		t.stopR, t.stopW = p[0], p[1]
	}

	return t
}

// Run answers notifications until the listener has no more to give.
//
// Returns when the descriptor is closed or the last filtered process is gone,
// which is how a step ends. Errors from a single notification are not fatal and
// are not silent either: each one marks the observation incomplete, because a
// call this engine could not interpret is a read it cannot rule out.
func (t *Tracer) Run() {
	// **The loop owns the memory descriptor, and only the loop.** `Close` is
	// called by whoever is waiting on the step, which is a different goroutine,
	// and closing it there was a data race against the handler reading it -
	// caught by `-race` in engine/fleet, not here, because it needs a step that
	// actually faults something in.
	//
	// Released here instead: the loop has several ways out and all of them come
	// through this return, which is what a defer is for.
	defer t.mem.forget()

	for {
		if !t.waitForWork() {
			return
		}

		n, err := receive(t.fd)
		if err != nil {
			t.stopped(fmt.Errorf("receiving a notification: %w", err))

			return
		}

		t.handled.Add(1)

		t.handle(n)

		// Always, and last. A notification left unanswered leaves the step
		// stopped in the kernel, so this happens whatever was made of it -
		// including nothing.
		err = respond(t.fd, n.ID)
		if err != nil {
			t.stopped(fmt.Errorf("answering notification %d: %w", n.ID, err))

			return
		}
	}
}

// stopped records why this loop gave up.
//
// **A servicer that stops is worse than one that never started.** The filter
// outlives it, so the next syscall the step makes is stopped in the kernel and
// nothing is coming to release it - and until this was recorded, that presented
// as a build hanging with no message anywhere, on either side, about why.
func (t *Tracer) stopped(err error) {
	t.stopMu.Lock()
	defer t.stopMu.Unlock()

	if t.stopErr != nil {
		return
	}

	t.stopErr = err

	// **Not a fault when the step was the only carrier.** A hang-up then means
	// the step exited, which every traced step does - reported, it puts a
	// "syscall tracer stopped" line in the log for every step of every build.
	// Where the guest filtered a thread of its own, the same event means that
	// thread is still filtered with nothing answering it, which is the hang
	// this print exists to name (E520, E521).
	//
	// Recorded either way: `Stopped` still returns it, so a caller that cares
	// is unaffected. Only the log is quiet.
	if t.soleCarrier && t.hungUp.Load() {
		return
	}

	// First one wins, here as in Stopped: a loop stops once, and a second line
	// reads as a second fault.
	w := t.Report
	if w == nil {
		w = os.Stderr
	}

	_, _ = fmt.Fprintf(w, "earth: syscall tracer stopped: %v\n", err)
}

// Servicing is closed once the notification loop is waiting for work.
//
// A step must not run under a filter nobody is answering, and this is how a
// caller waits to be sure. See the field for what happens when it does not.
func (t *Tracer) Servicing() <-chan struct{} { return t.servicing }

// Stopped is why the notification loop ended, or nil if it ended because it was
// asked to.
func (t *Tracer) Stopped() error {
	t.stopMu.Lock()
	defer t.stopMu.Unlock()

	return t.stopErr
}

// waitForWork blocks until a notification is ready or the tracer is stopped.
//
// Reports whether there is work. The listener is polled rather than read
// directly so that a stop can be noticed: `receive` blocks in an `ioctl` and
// nothing short of a notification brings it back.
//
// Without a stop pipe - which only happens if one could not be made - this waits
// on the listener alone and behaves as it did before, terminating when the last
// filtered process exits.
func (t *Tracer) waitForWork() bool {
	fds := []unix.PollFd{{Fd: int32(t.fd), Events: unix.POLLIN}} //nolint:gosec // a descriptor is not that large

	if t.stopR >= 0 {
		fds = append(fds, unix.PollFd{Fd: int32(t.stopR), Events: unix.POLLIN}) //nolint:gosec // ditto
	}

	// Announced before the first poll, not after: a caller waiting for this is
	// waiting to know that somebody is listening, and after the poll returns is
	// too late to be that promise.
	t.serviceOnce.Do(func() { close(t.servicing) })

	for {
		_, err := unix.Poll(fds, -1)
		if err == unix.EINTR {
			continue
		}

		if err != nil {
			t.stopped(fmt.Errorf("polling the notification listener: %w", err))

			return false
		}

		// Asked to stop - the only one of these that is not news, and only when
		// somebody actually asked. `Revents != 0` is also POLLERR, POLLHUP and
		// POLLNVAL, so a stop pipe closed by anything other than Close ends this
		// loop just as quietly and deadlocks the step; say so rather than let it
		// pass for a clean stop (E522).
		if len(fds) > 1 {
			if r := fds[1].Revents; r != 0 {
				if !t.closing.Load() {
					t.stopped(fmt.Errorf("the stop pipe reported %s while the step"+
						" was still running and nothing asked this loop to stop",
						pollEvents(r)))
				}

				return false
			}
		}

		// **The listener went away while a step was still filtered.** POLLHUP
		// on a notification fd means the kernel has no filtered task left, which
		// after a step has exited is ordinary and before it has is the beginning
		// of a deadlock: the filter outlives this loop, and the step's next
		// intercepted syscall stops in the kernel with nothing to release it.
		//
		// Recorded rather than guessed at. Three investigations named a cause
		// for this stall from a summary and were wrong each time; the loop now
		// says which of its exits it took (E521).
		if r := fds[0].Revents; r&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			// Recorded apart from the message, because whether this is ordinary
			// depends on who was carrying the filter and only the caller knows.
			// A guest that installed it on a thread of its own is still filtered
			// here and in trouble; a step that installed its own through the
			// shim is simply gone. See Tracer.HungUp.
			t.hungUp.Store(true)

			t.stopped(fmt.Errorf("the notification listener reported %s while the"+
				" step was still running", pollEvents(r)))

			return false
		}

		if fds[0].Revents&unix.POLLIN != 0 {
			return true
		}
	}
}

// handle records what one notification says, or that it could not be read.
func (t *Tracer) handle(n seccompNotif) {
	// The engine's own thread, not the step. Answered like any other - it is
	// stopped in the kernel and waiting - and recorded as nothing, because it is
	// nothing the step did.
	//
	// Not `lose` either: this is not a gap in what was observed, it is a call
	// that was never part of the observation. Declaring it incomplete would deny
	// an L2 hit to every step, permanently, for a file the step never opened.
	if t.mine != 0 && n.Pid == t.mine {
		return
	}

	// Architecture first, before the syscall number means anything. A process
	// may issue calls in another architecture's numbering and those numbers
	// **overlap ours** - i386's 5 is `open`, x86-64's 5 is `fstat` - so
	// consulting the table on a foreign call would look up a real entry and read
	// whichever argument that entry names. A confident, wrong path.
	//
	// The filter traps these deliberately rather than passing them (E205), and
	// this is what that is for: the gap gets declared.
	if n.Data.Arch != auditArch {
		t.lose(whyForeignArch)

		return
	}

	if _, ok := pathArg(n.Data.NR); !ok {
		// Trapped, and not something this engine knows how to read a path from.
		// It cannot happen while the filter and the table are built from the
		// same list, and if it ever does the honest answer is that something was
		// looked at and this engine cannot say what.
		t.lose(whyUnknownCall)

		return
	}

	// A file the step *writes* is not a file it read.
	//
	// `cat x > out` opens `out` with O_WRONLY|O_CREAT|O_TRUNC, and the tracer
	// sees a path being named like any other. Recorded as a read it becomes a
	// prediction naming the step's own output - which the base cannot contain,
	// so it is stale on the next build for ever: `1 of 2 predictions stale
	// (/w/out.txt is gone from the base)` (E217).
	//
	// Only write-*only* is skipped. O_RDWR may read, and recording a read that
	// did not happen costs a miss, while missing one that did costs a false hit -
	// so the doubtful case goes the safe way.
	if writeOnly(n) {
		return
	}

	path, err := observedPath(&t.mem, n)
	if err != nil {
		// Unreadable is not absent. The step named *something*; recording one
		// fewer path would be a claim this engine cannot make, so the whole
		// observation is declared incomplete instead (I3).
		//
		// The errno comes with it. `whyUnreadable` alone says a step will never
		// earn an L2 hit and not why - which is precisely the performance bug
		// nobody can find that the reasons were added for, one level down
		// (E209). An errno is a closed set, so this cannot grow without bound;
		// the path and the address are deliberately left out, because they
		// would.
		t.lose(whyUnreadable + ": " + errnoOf(err))

		return
	}

	t.fill(path)
	t.record(path, isOpenNR(n.Data.NR))
}

// fill fetches a path the step is about to open, if it is not here.
//
// **Lazy materialisation, and the whole of it.** The step is stopped in the
// kernel *before* the open happens, so a file fetched now is a file the syscall
// then finds. A snapshotter does this on a page fault; this does it on the
// syscall, with a prediction in front so that most files are already here
// (E289).
//
// A file that is already here costs one `Lstat` and nothing else, which is the
// case that has to stay cheap because a good prediction makes it the only case.
func (t *Tracer) fill(path string) {
	if t.Fill == nil {
		return
	}

	_, err := os.Lstat(path)
	if err == nil {
		return
	}

	err = t.Fill(path)
	if err == nil {
		return
	}

	// **A fetch that failed is not a file that is absent**, and the difference
	// is a wrong build rather than a slow one. A step that reads a file which
	// exists in its base, and is handed ENOENT because a peer went away, takes
	// the other branch and succeeds - producing a layer keyed as though the file
	// had been looked for and not found. Nothing errors and nothing is corrupt.
	//
	// So it is recorded as fatal and the step is failed by whoever is running
	// it. A file that is *genuinely* absent is not this: the filler says so by
	// succeeding without creating anything, and the syscall proceeds to its
	// honest ENOENT.
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.unfilled == nil {
		t.unfilled = fmt.Errorf("could not obtain %s: %w", path, err)
	}
}

// Unfilled is the first path this engine could not obtain for the step, if any.
//
// Not a count and not a list: the first failure is the one that made the step's
// view of its base a lie, and everything after it is downstream of that.
func (t *Tracer) Unfilled() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.unfilled
}

// record keeps a path, unless it is one that says nothing.
//
// `opened` separates a path the step *opened* from one it merely interrogated,
// which matters for a directory: opening one is how a step enumerates it, and
// stat'ing one is how it walks past on the way to something inside. Only the
// first needs the directory's contents in the key - see recordSightings.
func (t *Tracer) record(path string, opened bool) {
	// The root is not a read. "The filesystem has a root" decides no behaviour,
	// while `/`'s digest carries a mode, an owner and a timestamp that move
	// whenever anything at all is layered on the base - so recording it makes a
	// step stale on every base change there is, which is the opposite of what
	// the tier is for (E221).
	//
	// Not a gap either: nothing is lost, so the observation stays complete. The
	// copy path reached this first and stops its ancestor walk *above* the root
	// for the same reason.
	if path == "/" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.paths[path] = true

	if opened {
		if t.opened == nil {
			t.opened = map[string]bool{}
		}

		t.opened[path] = true
	}
}

// writeOnly reports that an open can only have written.
//
// The flags of an open sit one argument after its path - `open(path, flags)`,
// `openat(dirfd, path, flags)` - which is derived rather than tabulated, on the
// same argument as the directory descriptor: a second table is a second thing to
// fall out of step with the first.
//
// `openat2` is not covered and is treated as a read. Its third argument is a
// pointer to a `struct open_how` rather than a word, so the flags are in the
// target's memory; reading them is possible and is not done here, because the
// cost of being wrong in this direction is a miss.
func writeOnly(n seccompNotif) bool {
	i, ok := pathArg(n.Data.NR)
	if !ok || !isOpenNR(n.Data.NR) || n.Data.NR == openAt2NR {
		return false
	}

	flags := n.Data.Args[i+1]

	return flags&unix.O_ACCMODE == unix.O_WRONLY
}

// isOpenNR reports whether a syscall opens a path rather than interrogating it.
func isOpenNR(nr int32) bool {
	for _, o := range openers {
		if nr == int32(o) { //nolint:gosec // a syscall number fits
			return true
		}
	}

	return false
}

// errnoOf names the system error at the bottom of a failure, or its type.
func errnoOf(err error) string {
	if errno, ok := errors.AsType[unix.Errno](err); ok {
		return errno.Error()
	}

	if perr, ok := errors.AsType[*fs.PathError](err); ok {
		return perr.Err.Error()
	}

	return "no system error"
}

func (t *Tracer) lose(why string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.why[why] = true
}

// Sightings is what has been seen so far.
//
// Safe to call while Run is going; a caller wanting the whole of a step's
// sightings waits for Run to return first.
func (t *Tracer) Sightings() Sightings {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := Sightings{
		Paths:      make([]string, 0, len(t.paths)),
		Opened:     make([]string, 0, len(t.opened)),
		Incomplete: len(t.why) > 0,
		Why:        make([]string, 0, len(t.why)),
	}

	for p := range t.opened {
		out.Opened = append(out.Opened, p)
	}

	for p := range t.paths {
		out.Paths = append(out.Paths, p)
	}

	for w := range t.why {
		out.Why = append(out.Why, w)
	}

	// Both, because both are read out of maps and a map's order is not one.
	slices.Sort(out.Paths)
	slices.Sort(out.Opened)
	slices.Sort(out.Why)

	return out
}

// FromListener is a tracer that owns the listener it was handed.
//
// For the guest, which does not install its own filter when a shim installs one
// and sends it back: `NewTracer` takes a descriptor number, and an `*os.File`
// dropped after that call closes the descriptor from a finaliser, leaving the
// step stopped on a listener nobody holds (E215). Ownership is the difference.
func FromListener(f *os.File) *Tracer {
	t := fromFile(f)
	t.soleCarrier = true

	return t
}

// fromFile is a tracer that owns the file its listener came in.
//
// Keeping the file rather than its number is the whole point: see Tracer.listener.
func fromFile(f *os.File) *Tracer {
	t := NewTracer(int(f.Fd()))
	t.listener = f

	return t
}

// HungUp reports whether Run stopped because the listener hung up.
//
// **Ordinary or fatal depending on who held the filter**, which is why this is
// reported rather than judged. POLLHUP means the kernel has no filtered task
// left. Where the filter was installed by the guest on a thread of its own, that
// thread is still filtered and its next intercepted syscall will stop in the
// kernel with nothing to answer it (E520, E521). Where the step installed its
// own and handed the listener back, there is no filtered task because the step
// has exited - which is how every such step ends.
func (t *Tracer) HungUp() bool { return t.hungUp.Load() }

// Close stops Run and releases the listener.
//
// The stop side is closed first and on purpose: it is what wakes a blocked Run,
// and closing the listener first would leave Run in an `ioctl` on a descriptor
// that no longer exists - which is not woken by the close and is then woken by
// nothing at all.
func (t *Tracer) Close() error {
	// **Idempotent, because the deadlock fix below calls it early.** A stopped
	// tracer is closed the moment it stops, to let go of a step it can no longer
	// answer for, and the ordinary path closes it again when the step is over.
	// A second `unix.Close` of a raw descriptor is not harmless - the number is
	// reusable, and closing it twice can take away somebody else's file.
	if t.closed.Swap(true) {
		return nil
	}

	// Before the close, so Run can never see the wake-up without the reason for
	// it and call an orderly stop a fault.
	t.closing.Store(true)

	if t.stopW >= 0 {
		_ = unix.Close(t.stopW)
		t.stopW = -1
	}

	// Through the file when there is one, so its finaliser has nothing left to
	// do and cannot close a descriptor this number has since been reused for.
	if t.listener != nil {
		return t.listener.Close() //nolint:wrapcheck // os reports this verbatim
	}

	return unix.Close(t.fd)
}

// pollEvents names the bits a poll came back with, because "0x18" in a message
// is a number somebody then has to look up.
func pollEvents(r int16) string {
	var names []string

	for _, e := range []struct {
		bit  int16
		name string
	}{
		{unix.POLLERR, "POLLERR"},
		{unix.POLLHUP, "POLLHUP"},
		{unix.POLLNVAL, "POLLNVAL"},
		{unix.POLLIN, "POLLIN"},
	} {
		if r&e.bit != 0 {
			names = append(names, e.name)
		}
	}

	if len(names) == 0 {
		return "no events"
	}

	return strings.Join(names, "|")
}

// Handled is how many notifications this tracer has answered.
//
// Every trapped call, including the ones recognised as this engine's own and
// the ones whose path could not be read: the question it exists to answer is
// what the round trip was paid for, and it was paid for all of them.
func (t *Tracer) Handled() int { return int(t.handled.Load()) }
