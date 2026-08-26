package guest_test

import (
	"context"
	"os"
	osexec "os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EarthBuild/earthbuild/engine/guest"
)

// bin resolves a command through the host's PATH.
//
// The same reasoning `sh` already used, applied to everything else a step runs.
// These tests skipped on Linux until E122, and the first thing they hit when
// they started running was `sleep: command not found` on a machine where sleep
// is at `/home/…/.nix-profile/bin/sleep` - the step's shell has no PATH of its
// own, so a bare command name is a guess about the host's layout.
//
// Not an engine defect and not a reason to skip: a fixture that assumes
// /usr/bin is a fixture, and hard-coding it is the Linuxism `sh` exists to
// avoid.
func bin(t *testing.T, name string) string {
	t.Helper()

	p, err := osexec.LookPath(name)
	if err != nil {
		t.Skipf("no %s on this machine", name)
	}

	return p
}

func sh(t *testing.T) string {
	t.Helper()

	p, err := osexec.LookPath("sh")
	if err != nil {
		t.Skip("no shell here")
	}

	return p
}

// Output must arrive while the step is running, not when it finishes.
//
// A build that goes silent for four minutes and then prints everything is a
// build the user cannot tell from a hung one - which is the single most common
// reason people reach for `docker build --progress=plain`.
func TestOutputArrivesWhileTheStepRuns(t *testing.T) {
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Parallel()

	root := stepRoot(t)
	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	var (
		mu    sync.Mutex
		first time.Time
	)

	start := time.Now()

	code, _, err := c.ExecStream(context.Background(), h,
		[]string{sh(t), "-c", "echo early; " + bin(t, "sleep") + " 1; echo late"}, nil,
		func(chunk string, _ bool) {
			mu.Lock()
			defer mu.Unlock()

			if first.IsZero() && strings.Contains(chunk, "early") {
				first = time.Now()
			}
		})
	if err != nil {
		t.Fatal(err)
	}

	if code != 0 {
		t.Fatalf("step exited %d", code)
	}

	mu.Lock()
	defer mu.Unlock()

	if first.IsZero() {
		t.Fatal("no output arrived during the step")
	}

	if d := first.Sub(start); d > 700*time.Millisecond {
		t.Errorf("the first line arrived after %v; output is buffered until the step ends", d)
	}
}

// Concurrent steps' output must stay attributed to the step that produced it.
//
// Two steps run at once and interleave. Output that cannot be attributed is
// worse than no output: the user reads one step's error under another step's
// heading and debugs the wrong command.
func TestConcurrentOutputStaysAttributed(t *testing.T) {
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Parallel()

	root := stepRoot(t)
	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	var wg sync.WaitGroup

	for _, name := range []string{"alpha", "beta", "gamma"} {
		wg.Go(func() {
			h, err := c.Materialise(context.Background(), nil)
			if err != nil {
				t.Error(err)

				return
			}

			defer h.Release()

			var got strings.Builder

			_, _, err = c.ExecStream(context.Background(), h,
				[]string{sh(t), "-c", "for i in 1 2 3; do echo " + name + "; " + bin(t, "sleep") + " 0.02; done"}, nil,
				func(chunk string, _ bool) { got.WriteString(chunk) },
			)
			if err != nil {
				t.Error(err)

				return
			}

			// Every line this step received must be its own.
			for line := range strings.FieldsSeq(got.String()) {
				if line != name {
					t.Errorf("%s received a line belonging to %s", name, line)
				}
			}
		})
	}

	wg.Wait()
}

// The final result still carries the whole output, because a failing step's
// message is what the error is made of.
func TestStreamedStepsStillReturnTheirOutput(t *testing.T) {
	if !guest.NeedsIsolation(t) {
		return
	}

	t.Parallel()

	root := stepRoot(t)
	c := pairWith(t, &guest.Server{Mat: &fixedRootMat{root: root}, Unconfined: true})

	h, err := c.Materialise(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// t.Cleanup, not defer: a parent returns before its parallel subtests run,
	// so a deferred release takes the handle away from the tests that were
	// about to use it - "unknown handle h1", three subtests at once.
	t.Cleanup(func() { _ = h.Release() })

	code, out, err := c.Exec(context.Background(), h,
		[]string{sh(t), "-c", "echo to-stdout; echo to-stderr >&2; exit 3"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if code != 3 {
		t.Errorf("exit code is %d, want 3", code)
	}

	for _, want := range []string{"to-stdout", "to-stderr"} {
		if !strings.Contains(out, want) {
			t.Errorf("the returned output is missing %q:\n%s", want, out)
		}
	}
}

var _ = os.Getenv
