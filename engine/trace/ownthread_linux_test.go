//go:build linux

package trace

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

// The installing thread's own reads are not the step's.
//
// **`StartOnSelf` puts the engine and the step on one thread**, which is what
// lets a step be traced with no helper binary and nothing of the engine's inside
// the step's filesystem. The price is that the thread goes on doing the engine's
// work while the filter is live - `exec.Cmd` alone opens /dev/null on it for a
// nil Stdout - and every one of those opens would otherwise be recorded as
// something the step read (E211).
//
// Recording them would not merely be untidy. An observation naming files the
// step never opened is one no future base can satisfy, so the step could never
// earn an L2 hit again - and *not* a gap either, because declaring it incomplete
// denies the hit just as permanently. The engine's own calls are answered like
// any other and recorded as nothing.
//
// The rule was written twice and asserted nowhere. `keepalive_linux_test.go`
// says of its own open that "it will not appear in the sightings, and that is
// correct", and then does not look. A mutation sweep found the gap: disabling
// the skip left the whole package passing.
func TestTheInstallingThreadsOwnReadsAreNotTheSteps(t *testing.T) {
	// Not parallel: it locks a thread and installs a filter on it.
	seen := make(chan Sightings, 1)
	failed := make(chan error, 1)

	// A name this thread opens and nothing else does, so finding it in the
	// sightings can only mean the engine's own read was recorded.
	own := filepath.Join(t.TempDir(), "the-engines-own-file")

	err := os.WriteFile(own, []byte("x"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	park := parking(t)

	go func() {
		// Locked and never unlocked: a filter cannot be removed, so the thread
		// ends with this goroutine (E627).
		runtime.LockOSThread()

		tr, startErr := StartOnSelf()
		if startErr != nil {
			failed <- startErr

			return
		}

		go tr.Run()

		// The engine's own work, on the engine's own thread, while the filter
		// is live. This is the shape of what `exec.Cmd` does on this thread
		// without being asked.
		f, openErr := os.Open(own)
		if openErr == nil {
			_ = f.Close()
		}

		// Long enough for the notification to have been answered: the point is
		// what the tracer *did* with it, not whether it arrived.
		time.Sleep(200 * time.Millisecond)

		seen <- tr.Sightings()

		park()
	}()

	select {
	case startErr := <-failed:
		t.Skipf("no seccomp user notification here: %v", startErr)
	case got := <-seen:
		if slices.Contains(got.Paths, own) {
			t.Errorf("the engine's own open of %s was recorded as the step's"+
				"\n  an observation naming a file the step never opened is one"+
				" no base can satisfy, so the step earns no L2 hit ever again"+
				" (E211)\n  sightings: %v", own, got.Paths)
		}

		// The other half, and the reason this is not simply "record nothing":
		// a call the engine made is not a gap in what the step did, so calling
		// it incomplete would deny the hit just as permanently.
		if got.Incomplete {
			t.Errorf("the engine's own call was declared a gap in the step's"+
				" observation: %v", got.Why)
		}
	}
}
