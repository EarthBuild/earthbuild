//go:build linux

package trace

import (
	"os"
	"os/exec"
	"runtime"
	"slices"
	"testing"
	"time"
)

// The program a step runs is an input to that step.
//
// `RUN ./main` reads `/code/main` - that is what executing it *means* - and a
// base with a different `main` would produce a different result. Yet `execve` is
// how a program is read, and it is not an `open`: a filter watching opens and
// metadata never sees it.
//
// **This is a false-hit vector, not a gap in coverage.** A step that runs a
// dynamically linked binary records the libraries the loader opens and *not the
// binary itself*, so its observation is satisfied by any base carrying the same
// libc - including one where `/code/main` is a different program entirely. That
// is precisely the reuse I3 forbids (§3.4).
//
// It also explains a step in the corpus that observed nothing at all
// (`Earthfile:24: RUN ./main`, E219): a program whose only access to its
// filesystem is its own `execve` reads nothing this engine can see.
//
// Traced now, which supersedes the earlier decision to watch opens and metadata
// only - taken when the argument for exec was diagnostics, and the argument here
// is correctness (E220).
func TestTheProgramAStepRunsIsRecorded(t *testing.T) {
	program, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no `true` to exec: %v", err)
	}

	// **Not** through EvalSymlinks. `true` resolves to the multi-call
	// `coreutils` binary on this machine, which exits 1 when argv[0] is not an
	// applet name - and the tracer records the path `execve` was *given*, not
	// the file it ends at, so resolving here would assert the wrong string as
	// well as break the program.

	ready := make(chan *Tracer, 1)
	failed := make(chan error, 1)
	done := make(chan error, 1)

	go func() {
		// Locked and never unlocked (E206).
		runtime.LockOSThread()

		tr, err := StartOnSelf()
		if err != nil {
			failed <- err

			return
		}

		ready <- tr

		// Started from this thread, so the child inherits the filter (E211).
		done <- exec.Command(program).Run()

		select {}
	}()

	var tr *Tracer

	select {
	case err := <-failed:
		t.Skipf("no seccomp user notification here: %v", err)
	case tr = <-ready:
	}

	go tr.Run()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("running %s: %v", program, err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the child never finished")
	}

	// A beat for the last notifications to be handled.
	time.Sleep(200 * time.Millisecond)

	got := tr.Sightings()

	if !slices.Contains(got.Paths, program) {
		t.Errorf("a step ran %q and the tracer did not record it"+
			"\n  saw %d paths: %v"+
			"\n  the program is an input: a base carrying a different one at"+
			" the same path would satisfy this observation, which is the false"+
			" hit I3 forbids",
			program, len(got.Paths), first(got.Paths, 6))
	}

	_ = os.Getpid()
}

// first is a few of a slice, for a message that has to stay readable.
func first(s []string, n int) []string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}
