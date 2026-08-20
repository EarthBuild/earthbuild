//go:build linux

package trace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// filling runs body with a tracer that fills missing paths, and reports what it
// was asked for.
func filling(t *testing.T, fill func(string) error, body func()) *Tracer {
	t.Helper()

	ready := make(chan *Tracer, 1)
	failed := make(chan error, 1)
	finished := make(chan struct{})

	go func() {
		runtime.LockOSThread() // never unlocked: the thread ends with this goroutine

		// `install` rather than `StartOnSelf`: the latter records the engine's
		// own thread and skips its syscalls, which is right in the guest - where
		// the step is a child process - and wrong here, where the "step" is this
		// very thread. A tracer that skipped it would see nothing, which is
		// exactly what the first version of this test measured.
		fd, err := install(auditArch, traced)
		if err != nil {
			failed <- err

			return
		}

		tr := NewTracer(fd)
		tr.Fill = fill

		go tr.Run()

		ready <- tr
		body()
		close(finished)

		select {}
	}()

	var tr *Tracer

	select {
	case err := <-failed:
		t.Skipf("no seccomp user notification here: %v", err)
	case tr = <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("the filter was never installed")
	}

	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("the filtered work never finished")
	}

	return tr
}

// A path that is not there is fetched before the step sees it is not there.
//
// **This is lazy materialisation, and the whole of it.** A step opens a file, the
// kernel stops it before the open happens, the engine fetches what it asked for,
// and the syscall then proceeds and finds it. A snapshotter does this on a page
// fault; this engine does it on the syscall, with a prediction in front so most
// files are already here (E289).
func TestAMissingPathIsFilledBeforeTheStepSeesItIsMissing(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "arrives-late.txt")

	var (
		mu     sync.Mutex
		asked  []string
		opened bool
	)

	filling(t, func(p string) error {
		mu.Lock()
		asked = append(asked, p)
		mu.Unlock()

		if p != want {
			return errors.New("not this one")
		}

		return os.WriteFile(p, []byte("here after all\n"), 0o600) //nolint:gosec // a fixture
	}, func() {
		fd, err := unix.Openat(unix.AT_FDCWD, want, unix.O_RDONLY, 0)
		if err == nil {
			opened = true

			_ = unix.Close(fd)
		}
	})

	if !opened {
		mu.Lock()
		defer mu.Unlock()

		t.Errorf("the step could not open %s; asked for %v"+
			"\n  a fault-in that arrives after the syscall is no fault-in",
			want, asked)
	}
}

// A file already here is not fetched.
//
// The common case once a prediction is any good, and the one that decides
// whether this costs anything: a step that reads what it was predicted to read
// must not pay a lookup per open beyond the stat.
func TestAPathThatIsAlreadyHereIsNotFilled(t *testing.T) {
	dir := t.TempDir()
	here := filepath.Join(dir, "already.txt")

	err := os.WriteFile(here, []byte("present\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu    sync.Mutex
		asked int
	)

	filling(t, func(string) error {
		mu.Lock()
		asked++
		mu.Unlock()

		return nil
	}, func() {
		fd, err := unix.Openat(unix.AT_FDCWD, here, unix.O_RDONLY, 0)
		if err == nil {
			_ = unix.Close(fd)
		}
	})

	mu.Lock()
	defer mu.Unlock()

	if asked != 0 {
		t.Errorf("filled %d time(s) for a file that was already here", asked)
	}
}

// A fill that cannot be satisfied fails the step rather than letting it see
// ENOENT.
//
// **The hazard lazy materialisation introduces, and it is not a slow build - it
// is a wrong one.** A step that reads a file which exists in its base, and is
// handed "no such file" because a fetch failed, takes the other branch and
// succeeds. Nothing is corrupt, nothing errors, and the layer it produces is
// keyed as though the file had been read and absent.
//
// So a fill that fails is recorded as fatal, and the guest fails the step. A
// missing file that is *genuinely* missing is not this: the filler says so by
// succeeding without creating anything, and the syscall proceeds to its honest
// ENOENT.
func TestAFillThatFailsMakesTheStepFailRatherThanLie(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "unreachable.txt")

	boom := errors.New("the peer went away")

	tr := filling(t, func(p string) error {
		if p == gone {
			return boom
		}

		return nil
	}, func() {
		fd, err := unix.Openat(unix.AT_FDCWD, gone, unix.O_RDONLY, 0)
		if err == nil {
			_ = unix.Close(fd)
		}
	})

	err := tr.Unfilled()
	if err == nil {
		t.Fatal("a fetch that failed left the step to see ENOENT" +
			"\n  the step takes the other branch and produces a layer keyed as" +
			" though the file were absent from its base")
	}

	if !errors.Is(err, boom) {
		t.Errorf("%v; the reason has to survive, or nobody can tell a peer that"+
			" went away from a file that was never there", err)
	}
}
