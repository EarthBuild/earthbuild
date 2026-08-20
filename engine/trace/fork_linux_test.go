//go:build linux

package trace_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/EarthBuild/earthbuild/engine/trace"
)

// A child forked from a filtered thread inherits the filter.
//
// If this holds, the guest needs no helper binary at all - which matters,
// because `SysProcAttr.Chroot` means the child is chrooted *before* it execs, so
// a helper would have to exist inside the step's own filesystem. Putting one
// there changes what the step can see and what it might copy, which is a high
// price for an implementation detail.
//
// The alternative is this: a goroutine locks its thread, installs the filter,
// and starts the command from that same thread. Go's fork happens on the calling
// thread, a seccomp filter is inherited across fork, and `PR_SET_NO_NEW_PRIVS`
// carries it through the exec (E210) - so the step is traced and the rest of the
// guest is not.
//
// The cost, if it works, is one thread per traced step: filters accumulate, so
// the thread cannot be reused, and it must be left locked so the runtime
// destroys it (E206).
func TestAChildForkedFromAFilteredThreadIsTraced(t *testing.T) {
	program, err := exec.LookPath("cat")
	if err != nil {
		t.Skipf("no cat to exec: %v", err)
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "read-by-the-child-91ab.txt")

	err = os.WriteFile(target, []byte("contents\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		tracer *trace.Tracer
		err    error
	}

	out := make(chan result, 1)

	go func() {
		// Never unlocked: the filter cannot come off, so this thread must be
		// destroyed with the goroutine rather than returned to the pool.
		runtime.LockOSThread()

		tr, err := trace.StartOnSelf()
		if err != nil {
			out <- result{err: err}

			return
		}

		go tr.Run()

		// Started from *this* thread, which is the whole experiment.
		cmd := exec.Command(program, target)
		cmd.Stdout, cmd.Stderr = nil, nil

		err = cmd.Run()
		out <- result{tracer: tr, err: err}

		// Held, so the thread lives until the process does. A goroutine
		// returning here would be tidier and would take the listener with it.
		select {}
	}()

	got := <-out
	if got.err != nil {
		t.Skipf("could not run a filtered child: %v", got.err)
	}

	seen := got.tracer.Sightings()

	if !slices.Contains(seen.Paths, target) {
		t.Errorf("a child forked from a filtered thread read %q and was not"+
			" observed\n  saw %d paths: incomplete=%v %v"+
			"\n  the filter is not inherited across fork the way this design"+
			" assumes, and the guest needs a helper inside the step's root",
			target, len(seen.Paths), seen.Incomplete, seen.Why)
	}

	t.Logf("the child named %d paths", len(seen.Paths))
}

// What the engine's own thread opens is not what the step read.
//
// The filter is on the thread that installs it, so that thread's syscalls trap
// alongside the child's - and the thread belongs to the engine. Everything it
// touches between installing the filter and reaping the step would otherwise be
// recorded as a path the step named.
//
// It is not hypothetical. `exec.Cmd` with a nil `Stdout` opens `/dev/null` in
// the *parent*, on that very thread, so the plainest possible use of the tracer
// attributes `/dev/null` to every step that does not redirect its output. And a
// step's key would then depend on a file it never mentioned.
//
// The notification carries the pid that made the call, so the two are
// distinguishable - the fix is to ignore this process's own.
func TestTheEnginesOwnThreadIsNotTheStep(t *testing.T) {
	dir := t.TempDir()
	own := filepath.Join(dir, "opened-by-the-engine-2c4f.txt")

	err := os.WriteFile(own, []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan *trace.Tracer, 1)
	fail := make(chan error, 1)

	go func() {
		runtime.LockOSThread()

		tr, err := trace.StartOnSelf()
		if err != nil {
			fail <- err

			return
		}

		go tr.Run()

		// The engine's own business, on the filtered thread. No step involved.
		f, err := os.Open(own)
		if err == nil {
			_ = f.Close()
		}

		out <- tr

		select {}
	}()

	var tr *trace.Tracer

	select {
	case err := <-fail:
		t.Skipf("no seccomp user notification here: %v", err)
	case tr = <-out:
	}

	if slices.Contains(tr.Sightings().Paths, own) {
		t.Errorf("%q was opened by the engine on its own thread and recorded"+
			" as something the step read"+
			"\n  a step's key would depend on a file it never named", own)
	}
}
