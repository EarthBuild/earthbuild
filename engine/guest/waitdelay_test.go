package guest

import (
	osexec "os/exec"
	"testing"
	"time"
)

// A step whose grandchild outlives it still finishes.
//
// **`Wait` waits for the copying, not just the child.** When `Stdout` is not an
// `*os.File`, `os/exec` makes an OS pipe and a goroutine to drain it, and `Wait`
// returns only once that goroutine sees EOF - which needs *every* holder of the
// write end to close it. A process the step spawned in the background inherits
// that end, so a step that exits promptly can still leave the guest waiting for
// ever.
//
// This is not hypothetical. `go mod download` runs `git` for VCS fetches, and a
// build of this repository hung roughly one run in five to ten with the guest
// blocked in exactly this call and a second goroutine blocked in `io.Copy` -
// while the host waited for a reply that was never coming (E519).
//
// The fixture is the smallest thing that reproduces it: a shell that starts a
// sleeper in the background and exits immediately. Without a bound, this test
// does not fail - it hangs, which is what the bug does.
func TestAStepWhoseGrandchildOutlivesItStillFinishes(t *testing.T) {
	t.Parallel()

	cmd := osexec.Command("sh", "-c", "sleep 60 & exit 0")

	done := make(chan error, 1)

	go func() {
		_, err := run(cmd, func([]byte) {})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the step exited 0 and was reported as %v", err)
		}

	case <-time.After(stepWaitDelay + 20*time.Second):
		t.Fatal("the guest is still waiting for a pipe held by a process the step left behind")
	}
}

// An ordinary step is not delayed by the bound.
//
// The delay starts when the child exits, so a step that closes its pipes on the
// way out - which is every step that does not leave something behind - pays
// nothing.
func TestAnOrdinaryStepIsNotDelayed(t *testing.T) {
	t.Parallel()

	start := time.Now()

	out, err := run(osexec.Command("sh", "-c", "echo hello"), func([]byte) {})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if took := time.Since(start); took > stepWaitDelay {
		t.Errorf("an ordinary step took %v, which is the whole delay: the bound is being waited out", took)
	}

	if string(out) != "hello\n" {
		t.Errorf("output was %q", string(out))
	}
}
