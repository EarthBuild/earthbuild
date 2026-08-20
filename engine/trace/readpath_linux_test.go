//go:build linux

package trace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// observed is every path a filtered thread was seen to name, by syscall.
//
// A set per syscall rather than the last one, because the Go runtime runs on
// that thread too and opens its own files through the same numbers. Keeping only
// the most recent would make every assertion a race against whatever the runtime
// did last.
type observed struct {
	mu    sync.Mutex
	paths map[int32]map[string]bool
	fails map[int32][]error
}

func (o *observed) record(nr int32, path string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if err != nil {
		o.fails[nr] = append(o.fails[nr], err)

		return
	}

	if o.paths[nr] == nil {
		o.paths[nr] = map[string]bool{}
	}

	o.paths[nr][path] = true
}

// saw reports whether a syscall was seen naming this exact path.
func (o *observed) saw(nr int32, path string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.paths[nr][path]
}

// pathsFor is every path seen under one syscall, for a diagnostic.
func (o *observed) pathsFor(nr int32) []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	var out []string
	for p := range o.paths[nr] {
		out = append(out, p)
	}

	return out
}

// failures are the reads that did not yield a path at all.
func (o *observed) failures(nr int32) []error {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.fails[nr]
}

// watch installs the filter on a locked thread, runs body there, and reports
// every path the tracer recovered.
//
// The thread is never unlocked, so it is destroyed when this goroutine returns
// and the filter cannot reach anything else (E206).
func watch(t *testing.T, body func()) *observed {
	t.Helper()

	seen := &observed{paths: map[int32]map[string]bool{}, fails: map[int32][]error{}}
	ready := make(chan int, 1)
	failed := make(chan error, 1)
	finished := make(chan struct{})

	go func() {
		runtime.LockOSThread()

		fd, err := install(auditArch, traced)
		if err != nil {
			failed <- err

			return
		}

		ready <- fd
		body()
		close(finished)

		// Held open so the reader keeps answering while the assertions run;
		// the goroutine ends with the test and takes its thread with it.
		select {}
	}()

	var fd int

	select {
	case err := <-failed:
		t.Skipf("no seccomp user notification here: %v", err)
	case fd = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the filter was never installed")
	}

	go func() {
		for {
			n, err := receive(fd)
			if err != nil {
				return
			}

			if _, ok := pathArg(n.Data.NR); ok {
				// The resolved path, which is the one worth keying on: a bare
				// relative name means a different file in a different step.
				path, err := observedPath(n)

				// After the read, not before: this is what says the pid was not
				// recycled while the path was being fetched.
				if stillRunning(fd, n.ID) {
					seen.record(n.Data.NR, path, err)
				}
			}

			if err := respond(fd, n.ID); err != nil {
				return
			}
		}
	}()

	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("the filtered work never finished; a notification went unanswered")
	}

	return seen
}

// Every entry in the argument table is checked against its own syscall.
//
// The table says which argument of each traced call names a path, and an index
// one out reads a `flags` word as an address: no error, a plausible-looking
// string built from whatever was there, and a prediction keyed on it. Checking
// it against the manual page catches that on the day somebody reads the manual
// page.
//
// So each call is *made*, against a path chosen to be unmistakable, and the
// tracer's answer is compared with what was passed. A wrong index cannot produce
// the right string.
//
// The calls are made through `unix` rather than through `os`, deliberately: the
// standard library is free to satisfy `os.Stat` with whichever of `stat`,
// `newfstatat` or `statx` it prefers, and this needs the specific one named in
// the table.
func TestEachSyscallsPathArgumentIsTheOneRecovered(t *testing.T) {
	dir := t.TempDir()

	// A name that could not come from anywhere else, so a path recovered from
	// the wrong argument cannot coincide with it.
	target := filepath.Join(dir, "unmistakable-8f3a1c.txt")

	err := os.WriteFile(target, []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link-8f3a1c")

	err = os.Symlink(target, link)
	if err != nil {
		t.Fatal(err)
	}

	var stat unix.Stat_t

	buf := make([]byte, 256)

	// One entry per call this test can provoke, with the path it passes. Written
	// through `unix` rather than `os` on purpose: the standard library is free to
	// satisfy os.Stat with whichever of stat, newfstatat or statx it prefers, and
	// each of these needs a specific one.
	calls := []struct {
		nr   int32
		path string
		run  func()
	}{
		{unix.SYS_OPENAT, target, func() {
			fd, err := unix.Openat(unix.AT_FDCWD, target, unix.O_RDONLY, 0)
			if err == nil {
				_ = unix.Close(fd)
			}
		}},
		{unix.SYS_NEWFSTATAT, target, func() {
			_ = unix.Fstatat(unix.AT_FDCWD, target, &stat, 0)
		}},
		{unix.SYS_FACCESSAT, target, func() {
			_ = unix.Faccessat(unix.AT_FDCWD, target, unix.R_OK, 0)
		}},
		{unix.SYS_FACCESSAT2, target, func() {
			_ = unix.Faccessat2(unix.AT_FDCWD, target, unix.R_OK, 0)
		}},
		{unix.SYS_READLINKAT, link, func() {
			_, _ = unix.Readlinkat(unix.AT_FDCWD, link, buf)
		}},
		{unix.SYS_STATX, target, func() {
			var x unix.Statx_t

			_ = unix.Statx(unix.AT_FDCWD, target, 0, unix.STATX_BASIC_STATS, &x)
		}},
	}

	seen := watch(t, func() {
		for _, c := range calls {
			c.run()
		}
	})

	for _, c := range calls {
		if seen.saw(c.nr, c.path) {
			continue
		}

		// A wrong index usually lands on something unmapped and errors, which is
		// luck rather than design - `flags` could as easily point at mapped
		// memory and yield a plausible path. Both are failures here, and the
		// errors are printed because they name the address that was read: a
		// wrong index for an `*at` call shows up as 0xffffffffffffff9c, which is
		// AT_FDCWD.
		t.Errorf("syscall %d never named %q"+
			"\n  the argument index in pathArgs is wrong for this call,"+
			" or it was not trapped at all\n  failures: %v",
			c.nr, c.path, seen.failures(c.nr))
	}
}

// A pointer that is not a path is refused rather than truncated.
//
// `pathAt` reads until a terminator, and an address that was never a path has
// none within reach. Returning the first four kilobytes as a path would record a
// read of something enormous and imaginary; the caller has to be told it could
// not be read, so it can declare the observation incomplete instead of quietly
// recording one fewer.
func TestAnAddressThatIsNotAPathIsRefused(t *testing.T) {
	t.Parallel()

	// Address zero is never mapped, so the read fails outright.
	_, err := pathAt(uint32(os.Getpid()), 0)
	if !errors.Is(err, errUnreadable) {
		t.Errorf("reading a null pointer gave %v, want errUnreadable", err)
	}
}

// A path is read up to its terminator, and no further.
//
// The narrow case, away from the filter. `readPathFrom` takes an `io.ReaderAt`
// whose offsets happen to be addresses when it is `/proc/<pid>/mem`, so the
// stopping rule can be asserted against a buffer - where a wrong answer cannot
// be blamed on the target process, the pid, or the kernel.
func TestAPathIsReadUpToItsTerminator(t *testing.T) {
	t.Parallel()

	const offset = 40

	want := "/some/where/particular"

	// The path at a non-zero offset, with more after the terminator, so a reader
	// that ignored it would return something longer and be caught. Padded before
	// so an implementation that quietly starts at zero is caught too.
	mem := make([]byte, offset)
	mem = append(mem, want...)
	mem = append(mem, 0)
	mem = append(mem, "AND THEN SOME MORE"...)

	got, err := readPathFrom(bytes.NewReader(mem), offset)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	if got != want {
		t.Errorf("read %q, want %q", got, want)
	}

	if strings.Contains(got, "MORE") {
		t.Error("the read ran past the terminator")
	}
}

// A run of bytes with no terminator is not a very long path.
//
// It is an address that was never one, and the difference matters: returning the
// first four kilobytes would record a read of something enormous and imaginary,
// while refusing lets the caller declare the observation incomplete - which is
// what I3 asks for and what a truncation would quietly skip.
func TestAnUnterminatedRunIsNotAPath(t *testing.T) {
	t.Parallel()

	_, err := readPathFrom(bytes.NewReader(bytes.Repeat([]byte("a"), pathMax*2)), 0)
	if !errors.Is(err, errUnreadable) {
		t.Errorf("%v; a run with no terminator must be refused, not truncated", err)
	}
}
